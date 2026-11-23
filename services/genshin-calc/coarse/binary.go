package coarse

import (
	"bufio"
	"bytes"
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
	"strconv"
	"strings"
	"time"
)

// Binary runs the simulator program, one process per config.
type Binary struct {
	Path string
	// Threads caps how many cores each simulator process uses (GOMAXPROCS);
	// 0 leaves it to the simulator, which uses every core. Running several
	// uncapped processes at once oversubscribes the machine badly enough to
	// freeze it.
	Threads int
	// Timeout and MaxMemoryMB stop a simulation that runs away, e.g. a script
	// loop waiting for something that never happens: such a run grows by
	// about 40 MB a second until the machine runs out of memory. 0 means no
	// limit.
	Timeout     time.Duration
	MaxMemoryMB int
	fingerprint string
}

// LimitError means a simulation was stopped for exceeding a limit. Running
// it again would do the same, so it is cached like a success.
type LimitError struct{ Reason string }

func (e LimitError) Error() string { return "stopped: " + e.Reason }

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
	if _, err := b.run(ctx, dir, "-c", cfg, "-out", out); err != nil {
		return nil, err
	}
	return os.ReadFile(out)
}

// run runs the simulator in dir under the time and memory limits and returns
// its output. A failure carries the simulator's last line of output.
func (b *Binary) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if b.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, b.Timeout)
		defer cancel()
	}
	cmd := b.command(ctx, dir, args...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	overMemory := make(chan int, 1)
	if b.MaxMemoryMB > 0 {
		go watchMemory(cmd.Process, b.MaxMemoryMB, done, overMemory)
	}
	err := cmd.Wait()
	close(done)
	select {
	case mb := <-overMemory:
		return nil, LimitError{fmt.Sprintf("used over %d MB of memory (%d MB)", b.MaxMemoryMB, mb)}
	default:
	}
	if err != nil {
		if b.Timeout > 0 && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, LimitError{fmt.Sprintf("ran longer than %s", b.Timeout)}
		}
		return nil, fmt.Errorf("simulator failed: %s", lastLine(buf.Bytes(), err))
	}
	return buf.Bytes(), nil
}

// watchMemory checks the process's resident memory every half second and
// kills it once it passes maxMB, reporting the size on over.
func watchMemory(p *os.Process, maxMB int, done <-chan struct{}, over chan<- int) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
		}
		out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(p.Pid)).Output()
		if err != nil {
			continue // the process has just exited
		}
		kb, err := strconv.Atoi(strings.TrimSpace(string(out)))
		if err == nil && kb/1024 > maxMB {
			over <- kb / 1024
			p.Kill()
			return
		}
	}
}

// command prepares a simulator process in dir, with the thread cap.
func (b *Binary) command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, b.Path, args...)
	cmd.Dir = dir
	// Once the process is killed (time limit), stop waiting for its output
	// after a second: a child it started could otherwise keep the output
	// pipe open, and Wait with it, until that child exits on its own.
	cmd.WaitDelay = time.Second
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

// ReadCache returns the keys of the finished results in a cache file: the
// successes, and the runs stopped for exceeding a limit. A missing file is an
// empty cache. Other failures are not counted, so they are tried again on the
// next run.
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
		if r.Final {
			done[r.Key] = true // stopped for exceeding a limit: it would again
		}
	}
	return done, sc.Err()
}
