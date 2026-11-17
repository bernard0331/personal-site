package coarse

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// script writes an executable shell script standing in for the simulator.
func script(t *testing.T, body string) *Binary {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sim")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := NewBinary(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestTimeoutStopsARunawaySimulation(t *testing.T) {
	b := script(t, "sleep 30")
	b.Timeout = 300 * time.Millisecond
	start := time.Now()
	_, err := b.Run(context.Background(), "x")
	var limit LimitError
	if !errors.As(err, &limit) {
		t.Fatalf("want a LimitError, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("took %s", time.Since(start))
	}
}

func TestMemoryLimitStopsARunawaySimulation(t *testing.T) {
	// perl grows to about 300 MB and waits; the limit is 100 MB.
	b := script(t, `exec perl -e '$x = "a" x (300*1024*1024); sleep 30'`)
	b.MaxMemoryMB = 100
	_, err := b.Run(context.Background(), "x")
	var limit LimitError
	if !errors.As(err, &limit) {
		t.Fatalf("want a LimitError, got %v", err)
	}
}

func TestLimitFailuresAreCachedOtherFailuresNot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.jsonl")
	text := `{"key":"limit","error":"stopped: ran longer than 2m0s","final":true}
{"key":"crash","error":"simulator failed: boom"}
`
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	done, err := ReadCache(path)
	if err != nil {
		t.Fatal(err)
	}
	if !done["limit"] || done["crash"] {
		t.Errorf("got %v", done)
	}
}
