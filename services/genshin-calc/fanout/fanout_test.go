package fanout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bernard0331/personal-site/services/genshin-calc/coarse"
)

// fakeSim answers every config with a result whose DPS is the config's length.
type fakeSim struct{ fp string }

func (s fakeSim) Fingerprint() string { return s.fp }

func (s fakeSim) Run(_ context.Context, config string) ([]byte, error) {
	if strings.Contains(config, "boom") {
		return nil, errors.New("simulator failed: boom")
	}
	return []byte(fmt.Sprintf(`{"sim_version": "abc", "statistics": {
	  "iterations": 100, "dps": {"mean": %d, "sd": 10}, "duration": {"mean": 90},
	  "character_dps": [{"mean": 1}, {"mean": 2}, {"mean": 3}, {"mean": 4}],
	  "element_dps": {"pyro": {"mean": 7}}}}`, len(config))), nil
}

func jobs(n int) []coarse.Job {
	var js []coarse.Job
	for i := range n {
		js = append(js, coarse.Job{ID: fmt.Sprintf("r%d", i), Source: "test", Team: []string{"a", "b", "c", "d"},
			Config: strings.Repeat("x", 100+i)})
	}
	return js
}

// inProcess launches every shard as a goroutine running Work against the
// same store, with the given simulator.
func inProcess(t *testing.T, sim coarse.Simulator, skip map[int]bool) Launcher {
	return LaunchFunc(func(ctx context.Context, loc string, shards int) error {
		storeLoc, batch, err := SplitLocation(loc)
		if err != nil {
			return err
		}
		st, err := Open(storeLoc)
		if err != nil {
			return err
		}
		for i := range shards {
			if skip[i] {
				continue // a worker that never ran
			}
			go func() {
				if err := Work(context.Background(), st, batch, i, 2, func(Manifest) (coarse.Simulator, error) { return sim, nil }); err != nil {
					t.Logf("shard %d: %v", i, err)
				}
			}()
		}
		return nil
	})
}

func TestRunCollectsEveryResult(t *testing.T) {
	st := Dir(t.TempDir())
	sim := fakeSim{"fp1"}
	c := &Coordinator{Store: st, Launcher: inProcess(t, sim, nil), Shards: 4, MinPerShard: 5, Poll: 10 * time.Millisecond, Timeout: 10 * time.Second}
	in := jobs(23)
	var mu sync.Mutex
	got := map[string]coarse.Result{}
	missing, err := c.Run(context.Background(), in, sim, Quick, "", func(r coarse.Result) {
		mu.Lock()
		got[r.Key] = r
		mu.Unlock()
	}, nil)
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing %d, err %v", len(missing), err)
	}
	if len(got) != len(in) {
		t.Fatalf("got %d results, want %d", len(got), len(in))
	}
	for _, j := range in {
		r, ok := got[coarse.Key(j.Config, "fp1")]
		if !ok || r.ID != j.ID || r.DPS != float64(len(j.Config)) {
			t.Fatalf("result for %s: %+v", j.ID, r)
		}
	}
}

func TestRunReturnsWhatNoWorkerDid(t *testing.T) {
	st := Dir(t.TempDir())
	sim := fakeSim{"fp1"}
	c := &Coordinator{Store: st, Launcher: inProcess(t, sim, map[int]bool{1: true}), Shards: 3, MinPerShard: 1,
		Poll: 10 * time.Millisecond, Timeout: 500 * time.Millisecond}
	in := jobs(9)
	n := 0
	missing, err := c.Run(context.Background(), in, sim, Quick, "", func(coarse.Result) { n++ }, nil)
	if err == nil {
		t.Fatal("want a timeout error")
	}
	if n != 6 || len(missing) != 3 {
		t.Fatalf("saved %d, missing %d; want 6 and 3", n, len(missing))
	}
}

func TestWorkerRefusesAnotherSimulator(t *testing.T) {
	st := Dir(t.TempDir())
	c := &Coordinator{Store: st, Launcher: inProcess(t, fakeSim{"other"}, nil), Shards: 1, Poll: 10 * time.Millisecond, Timeout: 2 * time.Second}
	n := 0
	missing, err := c.Run(context.Background(), jobs(3), fakeSim{"fp1"}, Quick, "", func(coarse.Result) { n++ }, nil)
	if err != nil || n != 0 || len(missing) != 3 {
		t.Fatalf("saved %d, missing %d, err %v; want nothing saved and all 3 handed back", n, len(missing), err)
	}
}

func TestPlan(t *testing.T) {
	c := &Coordinator{Shards: 16, MinPerShard: 100}
	for _, tc := range []struct {
		kind string
		n    int
		want int
	}{{Quick, 4230, 16}, {Quick, 450, 5}, {Quick, 30, 1}, {Refine, 37, 37}, {Refine, 129, 129}, {Refine, 300, 256}, {Refine, 5, 5}} {
		if got := c.Plan(tc.kind, tc.n); got != tc.want {
			t.Errorf("Plan(%s, %d) = %d, want %d", tc.kind, tc.n, got, tc.want)
		}
	}
}

func TestSplitLocation(t *testing.T) {
	s, b, err := SplitLocation("gs://bucket/fanout/20261125-1")
	if err != nil || s != "gs://bucket/fanout" || b != "20261125-1" {
		t.Fatalf("%q %q %v", s, b, err)
	}
	if _, _, err := SplitLocation("nobatch"); err == nil {
		t.Fatal("want an error")
	}
}

func TestServiceFailureIsHandedBack(t *testing.T) {
	st := Dir(t.TempDir())
	// Nothing listens here, so every request fails at once.
	c := &Coordinator{Store: st, Launcher: &Service{URL: "http://127.0.0.1:1", Token: &IDToken{value: "x", expires: time.Now().Add(time.Hour)}},
		Shards: 2, MinPerShard: 1, Poll: 10 * time.Millisecond, Timeout: 20 * time.Second}
	start := time.Now()
	missing, err := c.Run(context.Background(), jobs(4), fakeSim{"fp1"}, Quick, "", func(coarse.Result) {}, nil)
	if err != nil || len(missing) != 4 {
		t.Fatalf("missing %d, err %v; want all 4 handed back", len(missing), err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("took %s: the coordinator waited instead of seeing the failures", time.Since(start))
	}
}

// A worker service stand-in: Cloud Run's "no available instance" HTML 500
// for the first two tries of each shard, then a worker that records how many
// requests were open at once and finishes the shard.
func TestServiceRetriesCloudRunAndThrottles(t *testing.T) {
	st := Dir(t.TempDir())
	sim := fakeSim{"fp1"}
	var mu sync.Mutex
	tries := map[int]int{}
	open, most := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ShardRequest
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		tries[req.Index]++
		n := tries[req.Index]
		mu.Unlock()
		if n <= 2 {
			w.Header().Set("Content-Type", "text/html")
			http.Error(w, "<html><head>500 Internal Server Error</head></html>", http.StatusInternalServerError)
			return
		}
		mu.Lock()
		open++
		most = max(most, open)
		mu.Unlock()
		defer func() { mu.Lock(); open--; mu.Unlock() }()
		storeLoc, batch, _ := SplitLocation(req.Location)
		s, _ := Open(storeLoc)
		time.Sleep(20 * time.Millisecond)
		if err := Work(r.Context(), s, batch, req.Index, 1, func(Manifest) (coarse.Simulator, error) { return sim, nil }); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"x"}`))
		}
	}))
	defer srv.Close()
	svc := &Service{URL: srv.URL, Token: &IDToken{value: "x", expires: time.Now().Add(time.Hour)}, Parallel: 2}
	c := &Coordinator{Store: st, Launcher: svc, MaxRefineShards: 6, Poll: 10 * time.Millisecond, Timeout: 30 * time.Second}
	n := 0
	missing, err := c.Run(context.Background(), jobs(6), sim, Refine, "", func(coarse.Result) { n++ }, nil)
	if err != nil || len(missing) != 0 || n != 6 {
		t.Fatalf("saved %d, missing %d, err %v; want all 6 after retries", n, len(missing), err)
	}
	if most > 2 {
		t.Fatalf("%d shards ran at once, want at most Parallel (2)", most)
	}
}

func TestServiceDoesNotRetryWorkerErrors(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// As the worker's own httpError answers (http.Error would set text/plain).
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"simulator missing"}`))
	}))
	defer srv.Close()
	svc := &Service{URL: srv.URL, Token: &IDToken{value: "x", expires: time.Now().Add(time.Hour)}, Parallel: 1}
	retry, err := svc.post(srv.Client(), []byte(`{}`), 0)
	if err == nil || retry || calls != 1 {
		t.Fatalf("retry %v, err %v, calls %d: a worker's own error must not be retried", retry, err, calls)
	}
}

func TestMeasureUsesTheCallersLabel(t *testing.T) {
	st := Dir(t.TempDir())
	var got Manifest
	l := LaunchFunc(func(ctx context.Context, loc string, shards int) error {
		storeLoc, batch, _ := SplitLocation(loc)
		s, _ := Open(storeLoc)
		for i := range shards {
			go Work(context.Background(), s, batch, i, 2, func(m Manifest) (coarse.Simulator, error) {
				got = m
				return fakeSim{"the-workers-own-build"}, nil // a different build: fine for measurements
			})
		}
		return nil
	})
	c := &Coordinator{Store: st, Launcher: l, Seed: 7, Poll: 10 * time.Millisecond, Timeout: 10 * time.Second}
	n := 0
	missing, err := c.Run(context.Background(), jobs(10), Label("measure"), Measure, "", func(coarse.Result) { n++ }, nil)
	if err != nil || len(missing) != 0 || n != 10 {
		t.Fatalf("saved %d, missing %d, err %v; want all 10", n, len(missing), err)
	}
	if got.Seed != 7 || got.Kind != Measure {
		t.Fatalf("manifest %+v: want kind measure, seed 7", got)
	}
}
