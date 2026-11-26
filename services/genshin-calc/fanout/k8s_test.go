package fanout

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestIndexedJobLaunch(t *testing.T) {
	var job map[string]any
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		host, _ := os.Hostname()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/genshin/pods/"+host:
			w.Write([]byte(`{"spec":{"containers":[{"name":"gcsfuse","image":"x"},{"name":"genshin-web","image":"repo/genshin-web:abc"}]}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/apis/batch/v1/namespaces/genshin/jobs":
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &job)
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{}`))
		default:
			http.Error(w, r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	saDir = dir
	os.WriteFile(filepath.Join(dir, "token"), []byte("tok\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "ca.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600)
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	t.Setenv("KUBERNETES_SERVICE_HOST", host)
	t.Setenv("KUBERNETES_SERVICE_PORT", port)

	j := &IndexedJob{Namespace: "genshin", ServiceAccount: "genshin-web", Args: []string{"-cpus", "8"},
		NodeSelector: map[string]string{"pool": "workers"}, Toleration: "workers", CPU: "7500m", Memory: "3Gi", Container: "genshin-web"}
	if err := j.Launch(context.Background(), "gs://b/gke/fanout/batch1", 3); err != nil {
		t.Fatal(err)
	}
	spec := job["spec"].(map[string]any)
	if spec["completionMode"] != "Indexed" || spec["completions"].(float64) != 3 {
		t.Fatalf("spec: %v", spec)
	}
	pod := spec["template"].(map[string]any)["spec"].(map[string]any)
	c := pod["containers"].([]any)[0].(map[string]any)
	if c["image"] != "repo/genshin-web:abc" {
		t.Fatalf("image %v, want the site's own", c["image"])
	}
	args := c["args"].([]any)
	if len(args) != 4 || args[2] != "-worker" || args[3] != "gs://b/gke/fanout/batch1" {
		t.Fatalf("args %v", args)
	}
	if pod["automountServiceAccountToken"] != false {
		t.Fatal("workers must not get an API token")
	}
}
