package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

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
	fs.BoolVar(&opt.fourStars, "four-stars", false, "assume R1 of every 4-star weapon the file leaves out (as in the passes)")
	fs.BoolVar(&opt.threeStars, "three-stars", false, "assume every 3-star weapon at R5, unlimited copies (as in the passes)")
	n := fs.Int("n", 10, "teams in the breakdown without an element rule")
	stats := fs.Bool("stats", false, "print each team's artifact stat lines (the full pass keeps the optimised substats)")
	skipFlagged := fs.Bool("skip-flagged", false, "leave out results flagged as waiting for energy or cooldowns")
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
	p, byID := problem(results, rule.inv, *skipFlagged)
	fmt.Printf("# Best teams\n\nFrom %s: %d teams.\n", fs.Arg(0), len(p.Teams))
	for _, o := range []solver.Objective{solver.Min, solver.Sum} {
		fmt.Printf("\n## One team per element, %s\n", o)
		printTeams(os.Stdout, p.ByElement(elements, o), byID, *stats)
	}
	for _, o := range []solver.Objective{solver.Min, solver.Sum} {
		fmt.Printf("\n## %d teams, any element, %s\n", *n, o)
		printTeams(os.Stdout, p.Best(*n, o), byID, *stats)
	}
	return nil
}

// problem turns results into solver teams: each team's best successful
// rotation. byID finds a team's result again.
func problem(results []coarse.Result, inv *weapons.Inventory, skipFlagged bool) (*solver.Problem, map[string]coarse.Result) {
	best := map[string]coarse.Result{}
	for _, r := range results {
		if r.Error != "" || (skipFlagged && len(r.Flags) > 0) {
			continue
		}
		k := coarse.TeamKey(r.Team)
		if b, ok := best[k]; !ok || r.DPS > b.DPS {
			best[k] = r
		}
	}
	p := &solver.Problem{Copies: map[string]int{}}
	byID := map[string]coarse.Result{}
	for _, r := range best {
		id := r.Source + "/" + r.ID
		byID[id] = r
		var ws []string
		for _, w := range r.Weapons {
			key := weaponKey(w)
			ws = append(ws, key)
			p.Copies[key] = inv.Copies(key)
		}
		p.Teams = append(p.Teams, solver.Team{
			ID: id, Members: r.Team, Weapons: ws, Element: coarse.MainElement(r), DPS: r.DPS,
		})
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
