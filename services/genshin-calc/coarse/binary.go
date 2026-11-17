package coarse

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Binary runs the simulator program, one process per config.
type Binary struct {
	Path string
	// Threads caps how many cores each simulator process uses (GOMAXPROCS);
	// 0 leaves it to the simulator, which uses every core. Running several
	// uncapped processes at once oversubscribes the machine badly enough to
	// freeze it.
	Threads     int
	fingerprint string
}

// NewBinary checks that the simulator exists and fingerprints it. The path is
// made absolute, because each simulation runs in its own temporary directory.
func NewBinary(path string) (*Binary, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return &Binary{Path: path, fingerprint: hex.EncodeToString(h.Sum(nil))[:12]}, nil
}

func (b *Binary) Fingerprint() string { return b.fingerprint }

// Run writes the config to a temporary directory, runs the simulator on it and
// returns its JSON result. The directory is removed afterwards.
func (b *Binary) Run(ctx context.Context, config string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "coarse-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	cfg, out := filepath.Join(dir, "config.txt"), filepath.Join(dir, "result.json")
	if err := os.WriteFile(cfg, []byte(config), 0o600); err != nil {
		return nil, err
	}
	cmd := b.command(ctx, dir, "-c", cfg, "-out", out)
	log, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("simulator failed: %s", lastLine(log, err))
	}
	return os.ReadFile(out)
}

// command prepares a simulator process in dir, with the thread cap.
func (b *Binary) command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, b.Path, args...)
	cmd.Dir = dir
	if b.Threads > 0 {
		cmd.Env = append(os.Environ(), fmt.Sprintf("GOMAXPROCS=%d", b.Threads))
	}
	return cmd
}

// lastLine is the simulator's last line of output, which holds its error message.
func lastLine(log []byte, err error) string {
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return l
	}
	return err.Error()
}

// ReadCache returns the keys of the successful results in a cache file. A
// missing file is an empty cache. Failed results are not counted, so they are
// tried again on the next run.
func ReadCache(path string) (map[string]bool, error) {
	done := map[string]bool{}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return done, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var r Result
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			// A run stopped mid-write leaves a partial last line; skip it.
			continue
		}
		if r.Error == "" && r.Key != "" {
			done[r.Key] = true
		}
	}
	return done, sc.Err()
}
