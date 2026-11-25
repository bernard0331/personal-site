package fanout

import (
	"context"
	"errors"
	"fmt"
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
	}{{Quick, 4230, 16}, {Quick, 450, 5}, {Quick, 30, 1}, {Refine, 18, 16}, {Refine, 5, 5}} {
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
