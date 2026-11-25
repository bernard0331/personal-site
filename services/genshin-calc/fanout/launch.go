package fanout

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
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
