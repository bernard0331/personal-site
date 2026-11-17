package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
	"github.com/bernard0331/personal-site/services/genshin-calc/coarse"
	"github.com/bernard0331/personal-site/services/genshin-calc/solver"
	"github.com/bernard0331/personal-site/services/genshin-calc/weapons"
)

const reportUsage = "usage: genshin-calc report [options] RESULTS ACCOUNT"

// The seven elements a team can be counted under. A team whose largest damage
// share is physical belongs to none of them.
var elements = []string{"pyro", "hydro", "electro", "cryo", "anemo", "geo", "dendro"}

// reportCmd chooses the player's best teams from a results file (the full
// pass's, or the quick pass's) and prints both breakdowns, each ranked both
// ways, as Markdown.
func reportCmd(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	var opt weaponAssumptions
	opt.flags(fs)
	within := fs.Float64("within", 0.10, "let the solver swap a member to a weapon whose trial score is within this fraction of theirs (needs -weapons)")
	variants := fs.Int("variants", 4, "weapon variants per team at most, the simulated one included")
	n := fs.Int("n", 10, "teams in the breakdown without an element rule")
	stats := fs.Bool("stats", false, "print each team's artifact stat lines (the full pass keeps the optimised substats)")
	skipFlagged := fs.Bool("skip-flagged", false, "leave out results flagged as waiting for energy or cooldowns")
	var picks coarse.Picks
	fs.IntVar(&picks.Top, "top", 100, "candidates: the best N teams overall")
	fs.IntVar(&picks.PerElement, "per-element", 20, "candidates: the best N teams of each main element")
	fs.IntVar(&picks.PerCharacter, "per-character", 3, "candidates: the best N teams each character is in")
	maxTime := fs.Duration("max-time", 5*time.Minute, "time per search before settling for the best found (0: no limit)")
	python := fs.String("highs", defaultHiGHS(), "a Python with highspy, to solve with HiGHS (empty: this package's slower branch and bound)")
	elementPicks := coarse.Picks{Top: 100, PerElement: 60, PerCharacter: 5}
	fs.Usage = func() { fmt.Fprintln(os.Stderr, reportUsage); fs.PrintDefaults() }
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
	results, err := coarse.ReadResults(fs.Arg(0))
	if err != nil {
		return err
	}
	var usable []coarse.Result
	for _, r := range results {
		if r.Error == "" && (!*skipFlagged || len(r.Flags) == 0) {
			usable = append(usable, r)
		}
	}
	// One team per element is fast on every team. Any N teams is not: it
	// runs on the candidates the full pass would pick.
	alt := alternatives{rule: rule, within: *within, max: *variants}
	start := time.Now()
	took := func() {
		fmt.Fprintf(os.Stderr, "search took %s\n", time.Since(start).Round(time.Millisecond))
		start = time.Now()
	}
	notProven := func() {
		fmt.Println("\n*The search reached its limit: this is the best found, not proven best.*")
	}

	if *python != "" {
		// HiGHS proves its answers best, over every team and weapon option.
		h := &solver.HiGHS{Python: *python, TimeLimit: *maxTime}
		all, byID := problem(usable, alt)
		fmt.Printf("# Best teams\n\nFrom %s: %d teams, %d team options with weapon variants, solved with HiGHS.\n",
			fs.Arg(0), coarse.Teams(asJobs(usable)), len(all.Teams))
		for _, o := range []solver.Objective{solver.Min, solver.Sum} {
			fmt.Printf("\n## One team per element, %s\n", o)
			r, err := all.ByElementHiGHS(h, elements, o)
			if err != nil {
				return err
			}
			printTeams(os.Stdout, r.Teams, byID, *stats)
			if took(); !r.Optimal {
				notProven()
			}
		}
		for _, o := range []solver.Objective{solver.Min, solver.Sum} {
			fmt.Printf("\n## %d teams, any element, %s\n", *n, o)
			r, err := all.BestHiGHS(h, *n, o)
			if err != nil {
				return err
			}
			printTeams(os.Stdout, r.Teams, byID, *stats)
			if took(); !r.Optimal {
				notProven()
			}
		}
		return nil
	}

	// Without HiGHS: this package's branch and bound, on candidates (it is
	// too slow on every team): the full pass's picks for any N teams, and a
	// wider set for one per element.
	all, byID := problem(coarse.Select(usable, elementPicks), alt)
	candidates, _ := problem(coarse.Select(usable, picks), alt)
	fmt.Printf("# Best teams\n\nFrom %s: %d teams; %d and %d team options (weapon variants included) searched.\n",
		fs.Arg(0), coarse.Teams(asJobs(usable)), len(all.Teams), len(candidates.Teams))
	deadline := func(p *solver.Problem) {
		if *maxTime > 0 {
			p.Deadline = time.Now().Add(*maxTime)
		}
	}
	for _, o := range []solver.Objective{solver.Min, solver.Sum} {
		deadline(all)
		fmt.Printf("\n## One team per element, %s\n", o)
		printTeams(os.Stdout, all.ByElement(elements, o), byID, *stats)
		if took(); all.Exhausted {
			notProven()
		}
	}
	for _, o := range []solver.Objective{solver.Min, solver.Sum} {
		deadline(candidates)
		fmt.Printf("\n## %d teams, any element, %s\n", *n, o)
		printTeams(os.Stdout, candidates.Best(*n, o), byID, *stats)
		if took(); candidates.Exhausted {
			notProven()
		}
	}
	return nil
}

// defaultHiGHS is the project's HiGHS Python (local/highs-venv, see the
// README) if it exists, else none.
func defaultHiGHS() string {
	const venv = "local/highs-venv/bin/python"
	if _, err := os.Stat(venv); err == nil {
		return venv
	}
	return ""
}

// asJobs keeps only the teams of results, for counting them.
func asJobs(rs []coarse.Result) []coarse.Job {
	js := make([]coarse.Job, len(rs))
	for i, r := range rs {
		js[i].Team = r.Team
	}
	return js
}

// alternatives says how far the solver may re-arm teams.
type alternatives struct {
	rule   *weaponRule
	within float64
	max    int
}

// problem turns results into solver teams: each team's best successful
// rotation, in its simulated weapons and in the weapon variants the trials
// rank close behind (DPS estimated from the trials). The variants share the
// team's four characters, so the solver takes at most one of them. byID finds
// a team's result again, with a variant's weapons and estimate filled in.
func problem(results []coarse.Result, alt alternatives) (*solver.Problem, map[string]coarse.Result) {
	best := map[string]coarse.Result{}
	for _, r := range results {
		if r.Error != "" {
			continue
		}
		k := coarse.TeamKey(r.Team)
		if b, ok := best[k]; !ok || r.DPS > b.DPS {
			best[k] = r
		}
	}
	inv := alt.rule.inv
	p := &solver.Problem{Copies: map[string]int{}}
	byID := map[string]coarse.Result{}
	for _, r := range best {
		id := r.Source + "/" + r.ID
		current := make([]string, len(r.Weapons))
		fixed := make([]bool, len(r.Weapons))
		for i, w := range r.Weapons {
			current[i] = weaponKey(w)
			// Favonius and Sacrificial weapons may be ones the rotation's
			// script waits on (see rotation.Member.WeaponLocked): keep them.
			fixed[i] = strings.HasPrefix(current[i], "favonius") || strings.HasPrefix(current[i], "sacrificial")
		}
		vs := []weapons.Variant{{Weapons: current, Factor: 1}}
		if alt.rule.ranking != nil && alt.max > 1 {
			vs = weapons.Alternatives(r.Team, current, fixed, alt.rule.ranking, inv, alt.within, alt.max)
		}
		for v, variant := range vs {
			vid, vr := id, r
			if v > 0 {
				vid = fmt.Sprintf("%s~%d", id, v)
				vr.DPS, vr.DPSSD = r.DPS*variant.Factor, r.DPSSD*variant.Factor
				vr.CharDPS = nil
				vr.Weapons = nil
				for i, w := range variant.Weapons {
					vr.Weapons = append(vr.Weapons, fmt.Sprintf("%s: %s R%d", r.Team[i], w, inv.Best(w)))
				}
				vr.Flags = append([]string{fmt.Sprintf("weapons changed after simulating: DPS estimated (%+.1f%%)", 100*(variant.Factor-1))}, r.Flags...)
			}
			byID[vid] = vr
			for _, w := range variant.Weapons {
				p.Copies[w] = inv.Copies(w)
			}
			p.Teams = append(p.Teams, solver.Team{
				ID: vid, Members: r.Team, Weapons: variant.Weapons, Element: coarse.MainElement(r), DPS: vr.DPS,
			})
		}
	}
	return p, byID
}

// weaponKey takes the weapon out of "member: weapon Rn".
func weaponKey(s string) string {
	_, rest, _ := strings.Cut(s, ": ")
	key, _, _ := strings.Cut(rest, " ")
	return key
}

func printTeams(w io.Writer, teams []solver.Team, byID map[string]coarse.Result, stats bool) {
	var dps []float64
	for _, t := range teams {
		if t.ID != "" {
			dps = append(dps, t.DPS)
		}
	}
	if len(dps) == 0 {
		fmt.Fprintln(w, "\nNo teams.")
		return
	}
	total, low := 0.0, dps[0]
	for _, d := range dps {
		total += d
		low = min(low, d)
	}
	fmt.Fprintf(w, "\nAverage %.0f, weakest %.0f.\n\n", total/float64(len(dps)), low)
	fmt.Fprintln(w, "| Element | DPS | Team (DPS each) | Weapons | Rotation | Notes |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|")
	for i, t := range teams {
		if t.ID == "" {
			fmt.Fprintf(w, "| %s | – | no team fits | | | |\n", elements[i])
			continue
		}
		r := byID[t.ID]
		var members []string
		for j, c := range r.Team {
			if j < len(r.CharDPS) {
				members = append(members, fmt.Sprintf("%s (%.0fk)", c, r.CharDPS[j]/1000))
			} else {
				members = append(members, c)
			}
		}
		var ws []string
		for _, x := range r.Weapons {
			_, rest, _ := strings.Cut(x, ": ")
			ws = append(ws, rest)
		}
		fmt.Fprintf(w, "| %s | %.0fk ±%.1fk | %s | %s | %s | %s |\n", t.Element, t.DPS/1000, r.DPSSD/1000,
			strings.Join(members, ", "), strings.Join(ws, ", "), t.ID, strings.Join(r.Flags, "; "))
	}
	if stats {
		for _, t := range teams {
			if r, ok := byID[t.ID]; ok && len(r.Stats) > 0 {
				fmt.Fprintf(w, "\n%s:\n\n```\n%s\n```\n", t.ID, strings.Join(r.Stats, "\n"))
			}
		}
	}
}
