package fanout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Open returns the store at loc: a gs://bucket/prefix URL or a directory.
func Open(loc string) (Store, error) {
	if rest, ok := strings.CutPrefix(loc, "gs://"); ok {
		bucket, prefix, _ := strings.Cut(rest, "/")
		if bucket == "" {
			return nil, fmt.Errorf("store %q: no bucket", loc)
		}
		return &GCS{Bucket: bucket, Prefix: strings.Trim(prefix, "/"), Token: &Token{}}, nil
	}
	return Dir(loc), nil
}

// Dir is a store in a local directory, for tests and running on one machine.
type Dir string

func (d Dir) Location() string { return string(d) }

func (d Dir) Put(_ context.Context, name string, data []byte) error {
	p := filepath.Join(string(d), filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p) // readers never see half a file, as with Cloud Storage
}

func (d Dir) Get(_ context.Context, name string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(string(d), filepath.FromSlash(name)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return b, err
}

func (d Dir) List(_ context.Context, prefix string) ([]string, error) {
	dir, base := filepath.Split(filepath.FromSlash(prefix))
	entries, err := os.ReadDir(filepath.Join(string(d), dir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), base) && !strings.HasSuffix(e.Name(), ".tmp") {
			out = append(out, filepath.ToSlash(filepath.Join(dir, e.Name())))
		}
	}
	sort.Strings(out)
	return out, nil
}

// GCS is a store in a Cloud Storage bucket, through its JSON API (standard
// library only). An object write replaces the whole object at once, so a
// reader never sees half of one.
type GCS struct {
	Bucket, Prefix string
	Token          *Token
	Client         *http.Client
}

func (g *GCS) Location() string {
	if g.Prefix == "" {
		return "gs://" + g.Bucket
	}
	return "gs://" + g.Bucket + "/" + g.Prefix
}

func (g *GCS) object(name string) string {
	if g.Prefix == "" {
		return name
	}
	return g.Prefix + "/" + name
}

func (g *GCS) do(ctx context.Context, method, u string, body io.Reader, ctype string) (*http.Response, error) {
	tok, err := g.Token.Get(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	c := g.Client
	if c == nil {
		c = &http.Client{Timeout: 2 * time.Minute}
	}
	return c.Do(req)
}

func (g *GCS) Put(ctx context.Context, name string, data []byte) error {
	u := "https://storage.googleapis.com/upload/storage/v1/b/" + url.PathEscape(g.Bucket) +
		"/o?uploadType=media&name=" + url.QueryEscape(g.object(name))
	resp, err := g.do(ctx, http.MethodPost, u, strings.NewReader(string(data)), "application/octet-stream")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return apiError("upload "+name, resp)
	}
	return nil
}

func (g *GCS) Get(ctx context.Context, name string) ([]byte, error) {
	u := "https://storage.googleapis.com/storage/v1/b/" + url.PathEscape(g.Bucket) + "/o/" +
		url.PathEscape(g.object(name)) + "?alt=media"
	resp, err := g.do(ctx, http.MethodGet, u, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode/100 != 2 {
		return nil, apiError("download "+name, resp)
	}
	return io.ReadAll(resp.Body)
}

func (g *GCS) List(ctx context.Context, prefix string) ([]string, error) {
	var out []string
	page := ""
	for {
		u := "https://storage.googleapis.com/storage/v1/b/" + url.PathEscape(g.Bucket) +
			"/o?fields=items(name),nextPageToken&prefix=" + url.QueryEscape(g.object(prefix))
		if page != "" {
			u += "&pageToken=" + url.QueryEscape(page)
		}
		resp, err := g.do(ctx, http.MethodGet, u, nil, "")
		if err != nil {
			return nil, err
		}
		var body struct {
			Items         []struct{ Name string }
			NextPageToken string
		}
		if resp.StatusCode/100 != 2 {
			err = apiError("list "+prefix, resp)
		} else {
			err = json.NewDecoder(resp.Body).Decode(&body)
		}
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, it := range body.Items {
			n := it.Name
			if g.Prefix != "" {
				n = strings.TrimPrefix(n, g.Prefix+"/")
			}
			out = append(out, n)
		}
		if body.NextPageToken == "" {
			return out, nil
		}
		page = body.NextPageToken
	}
}

func apiError(what string, resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("%s: %s: %s", what, resp.Status, strings.TrimSpace(string(b)))
}

// Token is an OAuth access token for Google APIs: from the metadata server
// on Cloud Run and GKE (the workload's own identity, no keys), or from
// gcloud on a developer's machine.
type Token struct {
	mu      sync.Mutex
	value   string
	expires time.Time
}

func (t *Token) Get(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.value != "" && time.Until(t.expires) > time.Minute {
		return t.value, nil
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token", nil)
	req.Header.Set("Metadata-Flavor", "Google")
	if resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req); err == nil {
		defer resp.Body.Close()
		var body struct {
			AccessToken string `json:"access_token"`
			ExpiresIn   int    `json:"expires_in"`
		}
		if resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&body) == nil && body.AccessToken != "" {
			t.value, t.expires = body.AccessToken, time.Now().Add(time.Duration(body.ExpiresIn)*time.Second)
			return t.value, nil
		}
	}
	out, err := exec.CommandContext(ctx, "gcloud", "auth", "print-access-token").Output()
	if err != nil {
		return "", fmt.Errorf("no access token: not on Google Cloud, and gcloud failed: %w", err)
	}
	t.value, t.expires = strings.TrimSpace(string(out)), time.Now().Add(30*time.Minute)
	return t.value, nil
}
