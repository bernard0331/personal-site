package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
	"github.com/bernard0331/personal-site/services/genshin-calc/coarse"
)

const fullUsage = "usage: genshin-calc full [options] DATABASE_CACHE ACCOUNT"

// fullCmd is the full pass. It picks the most promising teams from the quick
// pass's results, rebuilds their configs the same way, and simulates each
// again carefully: KQM Sim's substat optimiser chooses the substats, and the
// optimised config runs with many more iterations. Results are appended to
// their own cache, so an interrupted run (Ctrl-C) picks up where it stopped.
//
// The weapon and rotation options must match the quick pass's, so that the
// rebuilt configs are the ones the quick pass simulated.
func fullCmd(args []string) error {
	fs := flag.NewFlagSet("full", flag.ExitOnError)
	var opt weaponAssumptions
	opt.flags(fs)
	quick := fs.String("coarse", "local/coarse/results.jsonl", "the quick pass's results")
	out := fs.String("out", "", "results cache (JSON lines, appended to); default local/full/<mode>.jsonl")
	sim := fs.String("sim", "bin/kqmsim", "simulator binary")
	own := fs.String("own", "services/genshin-calc/rotations", "directory of this project's own rotation files (as in the quick pass)")
	mode := fs.String("mode", "kqm", "substats: kqm (KQM standard) or top1 (top 1% of builds)")
	iterations := fs.Int("iterations", 1000, "simulator iterations per team")
	workers := fs.Int("workers", 1, "teams to optimise at once (the optimiser already uses most cores)")
	var picks coarse.Picks
	fs.IntVar(&picks.Top, "top", 100, "take the best N teams overall")
	fs.IntVar(&picks.PerElement, "per-element", 20, "take the best N teams of each main element")
	fs.IntVar(&picks.PerCharacter, "per-character", 3, "take the best N teams each character is in")
	limit := fs.Int("limit", 0, "simulate at most this many teams (0 for all), for trying things out")
	dryRun := fs.Bool("dry-run", false, "list the chosen teams and stop")
	fs.Usage = func() { fmt.Fprintln(os.Stderr, fullUsage); fs.PrintDefaults() }
	fs.Parse(args)
	if fs.NArg() != 2 {
		fs.Usage()
		os.Exit(2)
	}
	options, ok := coarse.SubstatModes[*mode]
	if !ok {
		return fmt.Errorf("unknown -mode %q: want kqm or top1", *mode)
	}
	if *out == "" {
		*out = "local/full/" + *mode + ".jsonl"
	}

	bin, err := coarse.NewBinary(*sim)
	if err != nil {
		return fmt.Errorf("simulator: %w", err)
	}
	all, err := coarse.ReadResults(*quick)
	if err != nil {
		return fmt.Errorf("quick pass results: %w", err)
	}
	// Only results from this simulator build count: a rebuilt simulator may
	// rank teams differently.
	var results []coarse.Result
	for _, r := range all {
		if r.SimBinary == bin.Fingerprint() {
			results = append(results, r)
		}
	}
	if len(results) == 0 {
		return coarse.ErrNoResults
	}
	if old := len(all) - len(results); old > 0 {
		fmt.Fprintf(os.Stderr, "ignoring %d quick-pass results from other simulator builds\n", old)
	}
	chosen := coarse.Select(results, picks)
	fmt.Fprintf(os.Stderr, "%d quick-pass results, %d teams chosen for the full pass\n", len(results), len(chosen))

	// Rebuild each chosen rotation's config for the account.
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
	fieldable, _ := makeJobs(sources, acc, cat, rule, *iterations)
	byID := map[string]coarse.Job{}
	for _, j := range fieldable {
		byID[j.Source+"/"+j.ID] = j
	}
	var jobs []coarse.Job
	for _, r := range chosen {
		j, ok := byID[r.Source+"/"+r.ID]
		if !ok {
			fmt.Fprintf(os.Stderr, "  %s/%s is no longer fieldable with these options; skipped\n", r.Source, r.ID)
			continue
		}
		j.KeepStats = true
		jobs = append(jobs, j)
	}
	if *limit > 0 && *limit < len(jobs) {
		jobs = jobs[:*limit]
	}
	if *dryRun {
		quickDPS := map[string]float64{}
		for _, r := range chosen {
			quickDPS[r.Source+"/"+r.ID] = r.DPS
		}
		for _, j := range jobs {
			fmt.Printf("%.0f\t%s\t%s\t%s\n", quickDPS[j.Source+"/"+j.ID], j.Source, j.ID, strings.Join(j.Team, ","))
		}
		printElementCounts(chosen)
		return nil
	}
	return runJobs(jobs, &coarse.Optimiser{Bin: bin, Options: options}, *workers, *out, 1)
}

// printElementCounts says how many chosen teams have each main element.
func printElementCounts(chosen []coarse.Result) {
	n := map[string]int{}
	for _, r := range chosen {
		n[coarse.MainElement(r)]++
	}
	var els []string
	for e := range n {
		els = append(els, e)
	}
	sort.Strings(els)
	for _, e := range els {
		fmt.Fprintf(os.Stderr, "  %-10s %d teams\n", e, n[e])
	}
}
