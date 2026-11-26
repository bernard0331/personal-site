package fanout

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LaunchFunc adapts a function to a Launcher (tests use it to run workers
// in-process).
type LaunchFunc func(ctx context.Context, location string, shards int) error

func (f LaunchFunc) Launch(ctx context.Context, location string, shards int) error {
	return f(ctx, location, shards)
}

// Index is this worker's shard number, from whichever platform started it:
// a Cloud Run Job task, a Kubernetes Indexed Job pod, or Processes below.
func Index() (int, error) {
	for _, v := range []string{"CLOUD_RUN_TASK_INDEX", "JOB_COMPLETION_INDEX", "FANOUT_INDEX"} {
		if s := os.Getenv(v); s != "" {
			return strconv.Atoi(s)
		}
	}
	return 0, fmt.Errorf("no shard index: set CLOUD_RUN_TASK_INDEX, JOB_COMPLETION_INDEX or FANOUT_INDEX")
}

// CloudRunJob starts a Cloud Run Job execution with one task per shard.
// The job is deployed with the site's image (deploy/genshin-web/deploy.sh);
// each execution overrides its arguments with Args plus "-worker LOCATION"
// and its task count.
type CloudRunJob struct {
	Name   string   // projects/P/locations/R/jobs/J
	Args   []string // the worker's flags, e.g. -sim /app/kqmsim -cpus 8
	Token  *Token
	Client *http.Client
}

func (j *CloudRunJob) Launch(ctx context.Context, location string, shards int) error {
	tok, err := j.Token.Get(ctx)
	if err != nil {
		return err
	}
	args := append(append([]string{}, j.Args...), "-worker", location)
	body, _ := json.Marshal(map[string]any{
		"overrides": map[string]any{
			"containerOverrides": []map[string]any{{"args": args}},
			"taskCount":          shards,
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://run.googleapis.com/v2/"+j.Name+":run", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	c := j.Client
	if c == nil {
		c = &http.Client{Timeout: time.Minute}
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return apiError("run "+j.Name, resp)
	}
	return nil
}

// Processes runs each shard as a child process of Binary on this machine,
// for trying the whole path out locally.
type Processes struct {
	Binary string
	Args   []string
}

func (p *Processes) Launch(ctx context.Context, location string, shards int) error {
	for i := range shards {
		cmd := exec.Command(p.Binary, append(append([]string{}, p.Args...), "-worker", location)...)
		cmd.Env = append(os.Environ(), "FANOUT_INDEX="+strconv.Itoa(i))
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait()
	}
	return nil
}

// Service sends each shard to a Cloud Run service running workers (the
// site's image in -worker-serve mode, one request per instance at a time).
// A service starts new instances in seconds and scales to zero, where a
// Cloud Run Job's tasks waited 17-105 s to start (2026-11-26). Each request
// stays open while its shard runs; Launch returns once they are all sent.
type Service struct {
	URL    string // https://genshin-sim-....run.app
	Token  *IDToken
	Client *http.Client
	Log    func(format string, args ...any)
}

// ShardRequest is what Service posts to a worker.
type ShardRequest struct {
	Location string `json:"location"` // the batch: store location and batch id
	Index    int    `json:"index"`
}

func (s *Service) Launch(ctx context.Context, location string, shards int) error {
	tok, err := s.Token.Get(ctx, s.URL)
	if err != nil {
		return err
	}
	c := s.Client
	if c == nil {
		c = &http.Client{Timeout: 35 * time.Minute}
	}
	for i := range shards {
		body, _ := json.Marshal(ShardRequest{Location: location, Index: i})
		go func() {
			// Not ctx: the request must outlive Launch, and the coordinator
			// stops waiting on its own timeout.
			var err error
			for attempt, wait := 0, time.Second; ; attempt++ {
				var retry bool
				retry, err = s.post(c, tok, body, i)
				if err == nil || !retry || attempt == 60 || shardDone(location, i) {
					break
				}
				// Cloud Run turns a request away when it waited too long
				// for an instance (a burst of shards outruns start-up):
				// those instances are starting and take seconds, so ask
				// again soon (for up to about two minutes).
				time.Sleep(wait)
				wait = 2 * time.Second
			}
			if err == nil {
				return
			}
			if s.Log != nil {
				s.Log("fanout: shard %d: %v", i, err)
			}
			markFailed(location, i, err) // so the coordinator stops waiting for it
		}()
	}
	return nil
}

// post sends one shard request. retry says whether Cloud Run turned it away
// before a worker took it (no instance was free in time).
func (s *Service) post(c *http.Client, tok string, body []byte, i int) (retry bool, err error) {
	req, err := http.NewRequest(http.MethodPost, s.URL+"/shard", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return false, err // no answer at all: not the start-up case, which always answers
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 {
		return false, nil
	}
	err = apiError(fmt.Sprintf("shard %d", i), resp)
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return true, err
	case http.StatusInternalServerError: // Cloud Run's own "no available instance" comes as a 500 too
		return strings.Contains(err.Error(), "available instance"), err
	}
	return false, err
}

// shardDone reports whether shard i of the batch at location has a done marker.
func shardDone(location string, i int) bool {
	storeLoc, batch, err := SplitLocation(location)
	if err != nil {
		return false
	}
	st, err := Open(storeLoc)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = st.Get(ctx, shardName(batch, "done", i, "json"))
	return err == nil
}

// IDToken is an OpenID Connect identity token for calling a private Cloud
// Run service: from the metadata server (the workload's own identity), or
// from gcloud on a developer's machine.
type IDToken struct {
	mu      sync.Mutex
	value   string
	expires time.Time
}

func (t *IDToken) Get(ctx context.Context, audience string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.value != "" && time.Until(t.expires) > 5*time.Minute {
		return t.value, nil
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/identity?audience="+url.QueryEscape(audience), nil)
	req.Header.Set("Metadata-Flavor", "Google")
	if resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req); err == nil {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusOK && len(b) > 0 {
			t.value, t.expires = strings.TrimSpace(string(b)), time.Now().Add(55*time.Minute)
			return t.value, nil
		}
	}
	out, err := exec.CommandContext(ctx, "gcloud", "auth", "print-identity-token").Output()
	if err != nil {
		return "", fmt.Errorf("no identity token: not on Google Cloud, and gcloud failed: %w", err)
	}
	t.value, t.expires = strings.TrimSpace(string(out)), time.Now().Add(50*time.Minute)
	return t.value, nil
}

// markFailed writes shard i's done marker with err, for a worker that never
// got to write its own. The coordinator then hands the shard's jobs back.
func markFailed(location string, i int, err error) {
	storeLoc, batch, serr := SplitLocation(location)
	if serr != nil {
		return
	}
	st, serr := Open(storeLoc)
	if serr != nil {
		return
	}
	if shardDone(location, i) {
		return // the worker finished after all
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	b, _ := json.Marshal(Done{Shard: i, Error: err.Error()})
	st.Put(ctx, shardName(batch, "done", i, "json"), b)
}
