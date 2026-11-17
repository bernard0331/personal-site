package coarse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeSim answers every config with a fixed result whose DPS is the config's
// length, and fails on configs containing "boom". It records what it ran.
type fakeSim struct {
	mu  sync.Mutex
	ran []string
}

func (s *fakeSim) Fingerprint() string { return "fake" }

func (s *fakeSim) Run(_ context.Context, config string) ([]byte, error) {
	s.mu.Lock()
	s.ran = append(s.ran, config)
	s.mu.Unlock()
	if strings.Contains(config, "boom") {
		return nil, errors.New("simulator failed: boom")
	}
	return []byte(fmt.Sprintf(`{"sim_version": "abc", "statistics": {
	  "iterations": 100,
	  "dps": {"mean": %d, "sd": 10},
	  "duration": {"mean": 90},
	  "character_dps": [{"mean": 1}, {"mean": 2}, {"mean": 3}, {"mean": 4}],
	  "element_dps": {"pyro": {"mean": 7}, "physical": {"mean": 0}},
	  "failed_actions": [{"insufficient_energy": {"mean": 2.5}, "swap_cd": {"mean": 0.3}}, {}, {}, {}]
	}}`, len(config))), nil
}

func job(id, config string, team ...string) Job {
	return Job{ID: id, Source: "test", Team: team, Config: config}
}

func TestRunSimulatesEachJobOnceAndSkipsCached(t *testing.T) {
	sim := &fakeSim{}
	jobs := []Job{
		job("a", "config a", "x", "y", "z", "w"),
		job("b", "config bb", "x", "y", "z", "w"),
		job("a-again", "config a", "x", "y", "z", "w"), // same config: simulated once
		job("c", "config ccc", "x", "y", "z", "w"),
	}
	cached := map[string]bool{Key("config ccc", "fake"): true}

	var saved []Result
	skipped, err := Run(context.Background(), jobs, sim, 3, cached,
		func(r Result) error { saved = append(saved, r); return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 2 || len(saved) != 2 || len(sim.ran) != 2 {
		t.Fatalf("skipped %d, saved %d, ran %d; want 2, 2, 2", skipped, len(saved), len(sim.ran))
	}
	for _, r := range saved {
		if r.Key != Key(map[string]string{"a": "config a", "b": "config bb"}[r.ID], "fake") || r.SimBinary != "fake" {
			t.Errorf("%s: wrong key or fingerprint: %+v", r.ID, r)
		}
	}
}

func TestSummarise(t *testing.T) {
	sim := &fakeSim{}
	raw, _ := sim.Run(context.Background(), "12345")
	r, err := Summarise(job("a", "12345", "iansan", "b", "c", "d"), raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.DPS != 5 || r.Iterations != 100 || r.Duration != 90 || len(r.CharDPS) != 4 || r.SimVersion != "abc" {
		t.Errorf("wrong numbers: %+v", r)
	}
	if _, ok := r.ElementDPS["physical"]; ok || r.ElementDPS["pyro"] != 7 {
		t.Errorf("element DPS should keep only elements that dealt damage: %v", r.ElementDPS)
	}
	if r.Waits["insufficient_energy"] != 2.5 || r.Waits["swap_cd"] != 0.3 {
		t.Errorf("waits: %v", r.Waits)
	}
	if len(r.Flags) != 2 {
		t.Errorf("want flags for the energy wait and Iansan without movement, got %q", r.Flags)
	}
}

func TestFailuresAreSavedButNotCached(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "results.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	save := func(r Result) error {
		_, err := fmt.Fprintln(f, mustJSON(t, r))
		return err
	}
	jobs := []Job{job("ok", "fine", "a", "b", "c", "d"), job("bad", "boom", "a", "b", "c", "d")}
	if _, err := Run(context.Background(), jobs, &fakeSim{}, 2, nil, save, nil); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(f, `{"key": "partial line from a stopped ru`) // must be ignored
	f.Close()

	done, err := ReadCache(path)
	if err != nil {
		t.Fatal(err)
	}
	if !done[Key("fine", "fake")] || done[Key("boom", "fake")] || len(done) != 1 {
		t.Errorf("cache should hold only the success: %v", done)
	}
}

func TestCancelStopsWithoutCachingInterruptions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var jobs []Job
	for i := 0; i < 50; i++ {
		jobs = append(jobs, job(fmt.Sprint(i), fmt.Sprint("config ", i), "a", "b", "c", "d"))
	}
	saved := 0
	_, err := Run(ctx, jobs, &fakeSim{}, 2, nil, func(Result) error {
		saved++
		if saved == 5 {
			cancel()
		}
		return nil
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if saved >= len(jobs) {
		t.Errorf("all %d jobs ran despite the cancel", saved)
	}
}

func TestSetIterations(t *testing.T) {
	if got := SetIterations("options swap_delay=12 iteration=1000;\nx", 100); got != "options swap_delay=12 iteration=100;\nx" {
		t.Errorf("replace: %q", got)
	}
	if got := SetIterations("x", 50); !strings.HasPrefix(got, "options iteration=50;\n") {
		t.Errorf("add: %q", got)
	}
}

func mustJSON(t *testing.T, r Result) string {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
