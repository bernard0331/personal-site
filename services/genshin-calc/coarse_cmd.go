package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
	"github.com/bernard0331/personal-site/services/genshin-calc/coarse"
	"github.com/bernard0331/personal-site/services/genshin-calc/good"
	"github.com/bernard0331/personal-site/services/genshin-calc/rotation"
)

const coarseUsage = "usage: genshin-calc coarse [options] DATABASE_CACHE ACCOUNT"

// coarseCmd simulates every single-target rotation the account can field, from
// a cached KQM Sim Database file and this project's own rotation files, and
// appends the results to a JSONL cache. Results already in the cache are
// skipped, so an interrupted run (Ctrl-C) picks up where it stopped.
func coarseCmd(args []string) error {
	fs := flag.NewFlagSet("coarse", flag.ExitOnError)
	var opt weaponAssumptions
	opt.flags(fs)
	out := fs.String("out", "local/coarse/results.jsonl", "results cache (JSON lines, appended to)")
	sim := fs.String("sim", "bin/kqmsim", "simulator binary")
	own := fs.String("own", "services/genshin-calc/rotations", "directory of this project's own rotation files (empty for none)")
	iterations := fs.Int("iterations", 100, "simulator iterations per rotation")
	lim := limitFlags(fs, 2*time.Minute)
	workers := fs.Int("workers", 0, "simulations to run at once (default: one per core in -cpus)")
	limit := fs.Int("limit", 0, "simulate at most this many rotations (0 for all), for trying things out")
	dryRun := fs.Bool("dry-run", false, "list what would be simulated and stop")
	fs.Usage = func() { fmt.Fprintln(os.Stderr, coarseUsage); fs.PrintDefaults() }
	fs.Parse(args)
	if fs.NArg() != 2 {
		fs.Usage()
		os.Exit(2)
	}

	cat, err := catalog.Load()
	if err != nil {
		return err
	}
	acc, _, err := readAccount(fs.Arg(1), cat)
	if err != nil {
		return err
	}
	rule, err := opt.rule(acc, cat)
	if err != nil {
		return err
	}

	sources, err := readRotations(fs.Arg(0), *own)
	if err != nil {
		return err
	}
	jobs, skipped := makeJobs(sources, acc, cat, rule, *iterations)
	fmt.Fprintf(os.Stderr, "%d rotations read; %d can be fielded (%d distinct teams)\n",
		len(sources), len(jobs), coarse.Teams(jobs))
	for _, s := range skipped {
		fmt.Fprintf(os.Stderr, "  skipped %4d: %s\n", s.n, s.reason)
	}
	if *limit > 0 && *limit < len(jobs) {
		jobs = jobs[:*limit]
	}
	if *dryRun {
		for _, j := range jobs {
			fmt.Printf("%s\t%s\t%s\n", j.Source, j.ID, strings.Join(j.Team, ","))
		}
		return nil
	}

	bin, err := coarse.NewBinary(*sim)
	if err != nil {
		return fmt.Errorf("simulator: %w", err)
	}
	n := share(bin, lim, *workers)
	return runJobs(jobs, bin, n, *out, 25)
}

// limits are the resources a run may use.
type limits struct {
	cpus      *int
	timeout   *time.Duration
	maxMemory *int
}

// limitFlags registers -cpus, -timeout and -max-memory. timeout is the
// default time limit for one simulation.
func limitFlags(fs *flag.FlagSet, timeout time.Duration) limits {
	return limits{
		cpus:      fs.Int("cpus", max(1, runtime.NumCPU()-2), "cores to use in all (default: all but two)"),
		timeout:   fs.Duration("timeout", timeout, "stop a simulation that runs longer than this"),
		maxMemory: fs.Int("max-memory", 400, "stop a simulation that uses more than this many MB"),
	}
}

// share splits the cores between workers simulations at once (0 workers: one
// per core), caps each simulator process to its share and sets the limits.
// It returns the number of workers. With every worker at its memory limit,
// the run stays within workers x max-memory.
func share(bin *coarse.Binary, l limits, workers int) int {
	bin.Timeout, bin.MaxMemoryMB = *l.timeout, *l.maxMemory
	cpus := max(1, *l.cpus)
	if workers <= 0 {
		workers = cpus
	}
	bin.Threads = max(1, cpus/workers)
	return workers
}

// runJobs simulates jobs not already in the results cache at out, appending
// each result as it finishes, and reports progress every `every` results.
// Ctrl-C stops it; the results so far stay in the cache, so running the same
// command again continues.
func runJobs(jobs []coarse.Job, sim coarse.Simulator, workers int, out string, every int) error {
	cached, err := coarse.ReadCache(out)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	save := func(r coarse.Result) error {
		if err := enc.Encode(r); err != nil {
			return err
		}
		return w.Flush() // one line at a time, so a stopped run keeps what it finished
	}

	// Below normal priority, so the machine stays usable during a long run;
	// the simulator processes inherit it.
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 10)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	start := time.Now()
	report := func(p coarse.Progress) {
		if p.Done%every != 0 && p.Done != p.Total {
			return
		}
		elapsed := time.Since(start)
		left := time.Duration(float64(elapsed) / float64(p.Done) * float64(p.Total-p.Done))
		fmt.Fprintf(os.Stderr, "%d/%d simulated, %d failed, %s elapsed, about %s left\n",
			p.Done, p.Total, p.Failed, elapsed.Round(time.Second), left.Round(time.Second))
	}
	n, err := coarse.Run(ctx, jobs, sim, workers, cached, save, report)
	fmt.Fprintf(os.Stderr, "%d already in %s; simulator build %s\n", n, out, sim.Fingerprint())
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "stopped; run the same command again to continue")
		return nil
	}
	return err
}

// source is one rotation to consider, before checking the account can field it.
type source struct {
	id, from string
	config   string
	targets  int
}

// readRotations reads the cached database (one JSON object per line) and the
// own rotation files (*.txt in dir).
func readRotations(dbPath, dir string) ([]source, error) {
	var out []source
	f, err := os.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var e struct {
			ID      string `json:"id"`
			Config  string `json:"config"`
			Targets int    `json:"targets"`
		}
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("%s: %w", dbPath, err)
		}
		out = append(out, source{id: e.ID, from: "kqm-db", config: e.Config, targets: e.Targets})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if dir == "" {
		return out, nil
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.txt"))
	if err != nil {
		return nil, err
	}
	for _, p := range files {
		text, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		id := strings.TrimSuffix(filepath.Base(p), ".txt")
		out = append(out, source{id: id, from: "own", config: string(text), targets: 1})
	}
	return out, nil
}

type skipReason struct {
	reason string
	n      int
}

// makeJobs keeps the single-target rotations the account can field and writes
// each one's config. It also says how many were left out, and why.
func makeJobs(sources []source, acc *good.Account, cat *catalog.Catalog, rule *weaponRule, iterations int) ([]coarse.Job, []skipReason) {
	var jobs []coarse.Job
	reasons := map[string]int{}
	for _, s := range sources {
		if s.targets != 1 {
			reasons["not single-target"]++
			continue
		}
		rot, err := rotation.Parse(s.config, cat)
		if err != nil {
			reasons["cannot be read: "+err.Error()]++
			continue
		}
		cfg, weapons, err := buildConfig(rot, acc, cat, rule)
		var missing errMissingCharacter
		var locked errLockedWeapon
		switch {
		case errors.As(err, &missing):
			reasons["account lacks a member"]++
			continue
		case errors.As(err, &locked):
			reasons["account lacks a weapon the script depends on"]++
			continue
		case err != nil:
			reasons[err.Error()]++
			continue
		}
		jobs = append(jobs, coarse.Job{
			ID: s.id, Source: s.from, Team: rot.Team(), Weapons: weapons,
			Config: coarse.SetIterations(cfg, iterations),
		})
	}
	var list []skipReason
	for r, n := range reasons {
		list = append(list, skipReason{r, n})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].n > list[j].n })
	if len(list) > 8 {
		rest := 0
		for _, s := range list[8:] {
			rest += s.n
		}
		list = append(list[:8], skipReason{"other reasons", rest})
	}
	return jobs, list
}
