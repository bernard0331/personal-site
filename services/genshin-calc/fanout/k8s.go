package fanout

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// IndexedJob starts a Kubernetes Indexed Job with one pod per shard, from
// inside the cluster: the site's pod calls the API server with its own
// service account token, which an RBAC Role allows to manage Jobs in its
// namespace and read its own pod, nothing else (deploy/base/genshin-web).
//
// Each pod runs the site's image (read from the site's own pod, so a canary's
// workers run the canary's build) in -worker mode on the worker node pool,
// and finds its shard in JOB_COMPLETION_INDEX. Pods beyond what the pool
// holds wait for a node: the Job is a queue, and the cluster autoscaler adds
// nodes while pods are waiting.
type IndexedJob struct {
	Namespace      string
	ServiceAccount string            // the workers' Kubernetes service account (bucket access)
	Args           []string          // the worker's flags, e.g. -sim /app/kqmsim -cpus 8
	NodeSelector   map[string]string // the worker pool's label
	Toleration     string            // the worker pool's taint key
	CPU, Memory    string            // each worker pod's requests, e.g. "7", "3Gi"
	Container      string            // the site's container name, to read its image

	image string
}

// saDir is where Kubernetes mounts the pod's service account token and CA
// (a variable for tests).
var saDir = "/var/run/secrets/kubernetes.io/serviceaccount"

func (j *IndexedJob) client() (*http.Client, string, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" {
		return nil, "", fmt.Errorf("not in a Kubernetes pod")
	}
	ca, err := os.ReadFile(saDir + "/ca.crt")
	if err != nil {
		return nil, "", err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca)
	c := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	return c, "https://" + net.JoinHostPort(host, port), nil
}

func (j *IndexedJob) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	c, base, err := j.client()
	if err != nil {
		return nil, err
	}
	tok, err := os.ReadFile(saDir + "/token") // projected and rotated by the kubelet: read it each time
	if err != nil {
		return nil, err
	}
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+string(bytes.TrimSpace(tok)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out bytes.Buffer
	out.ReadFrom(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%s %s: %s: %.300s", method, path, resp.Status, out.String())
	}
	return out.Bytes(), nil
}

// ownImage reads the image of the site's container from its own pod.
func (j *IndexedJob) ownImage(ctx context.Context) (string, error) {
	if j.image != "" {
		return j.image, nil
	}
	pod, _ := os.Hostname() // a pod's hostname is its name
	b, err := j.do(ctx, http.MethodGet, "/api/v1/namespaces/"+j.Namespace+"/pods/"+pod, nil)
	if err != nil {
		return "", err
	}
	var p struct {
		Spec struct {
			Containers []struct{ Name, Image string }
		}
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return "", err
	}
	for _, c := range p.Spec.Containers {
		if c.Name == j.Container {
			j.image = c.Image
			return c.Image, nil
		}
	}
	return "", fmt.Errorf("pod %s has no container %q", pod, j.Container)
}

func (j *IndexedJob) Launch(ctx context.Context, location string, shards int) error {
	image, err := j.ownImage(ctx)
	if err != nil {
		return fmt.Errorf("reading the site's image: %w", err)
	}
	_, batch, _ := SplitLocation(location)
	args := append(append([]string{}, j.Args...), "-worker", location)
	f := false
	t := true
	uid := int64(10001)
	job := map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata": map[string]any{
			"generateName": "genshin-sim-",
			"labels":       map[string]string{"app": "genshin-sim"},
			"annotations":  map[string]string{"personal-site/batch": batch},
		},
		"spec": map[string]any{
			"completionMode":          "Indexed",
			"completions":             shards,
			"parallelism":             shards, // pods the pool can't hold yet wait for a node
			"backoffLimitPerIndex":    1,
			"maxFailedIndexes":        shards,
			"activeDeadlineSeconds":   1800,
			"ttlSecondsAfterFinished": 300, // the Job and its pods are deleted 5 minutes after
			"template": map[string]any{
				"metadata": map[string]any{
					"labels": map[string]string{"app": "genshin-sim"},
					// With the service mesh on, an Envoy sidecar would keep
					// every worker pod running after its shard is done.
					"annotations": map[string]string{"sidecar.istio.io/inject": "false"},
				},
				"spec": map[string]any{
					"restartPolicy":                "Never",
					"serviceAccountName":           j.ServiceAccount,
					"automountServiceAccountToken": false, // workers never call the Kubernetes API
					"nodeSelector":                 j.NodeSelector,
					"tolerations":                  []map[string]string{{"key": j.Toleration, "operator": "Exists", "effect": "NoSchedule"}},
					"securityContext": map[string]any{
						"runAsNonRoot": t, "runAsUser": uid, "runAsGroup": uid,
						"seccompProfile": map[string]string{"type": "RuntimeDefault"},
					},
					"containers": []map[string]any{{
						"name":  "worker",
						"image": image,
						"args":  args,
						"resources": map[string]any{
							"requests": map[string]string{"cpu": j.CPU, "memory": j.Memory},
							"limits":   map[string]string{"memory": "6Gi"},
						},
						"securityContext": map[string]any{
							"allowPrivilegeEscalation": f,
							"capabilities":             map[string]any{"drop": []string{"ALL"}},
						},
					}},
				},
			},
		},
	}
	_, err = j.do(ctx, http.MethodPost, "/apis/batch/v1/namespaces/"+j.Namespace+"/jobs", job)
	return err
}
