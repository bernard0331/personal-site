package main

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
	"github.com/bernard0331/personal-site/services/genshin-calc/coarse"
	"github.com/bernard0331/personal-site/services/genshin-calc/good"
	"github.com/bernard0331/personal-site/services/genshin-calc/optimizer"
)

// A refined run starts as a quick run, then simulates its most promising teams
// again the way the command line's full pass does: KQM Sim's substat
// optimiser picks each member's substats for the player's weapons and
// constellations, and the optimised config runs with many more iterations.
// The solver then chooses the teams again from the refined numbers only.
//
// Which teams: every team the quick run chose (in any breakdown, with the
// weapons it chose), plus the best quick-pass teams by refineLevels. On the
// owner's account (2026-11-22, 366 teams refined by the full pass), the quick
// run's own choices alone gave the same five-team breakdown and the same
// weakest team per element as all 366, and an element average within 2%; the
// extra teams are there because a quick run's numbers are rough (15
// iterations) and other accounts differ.

// refineLevels are the light and standard runs' settings, and a thorough
// run's starting point.
//
// Iterations: the substat optimiser takes about 85% of a refined team's
// time and the final simulation the rest. Over the full pass's 366 refined
// teams (2026-11-25), DPS varies by a median 2.4% from one iteration to the
// next, so 250 iterations give a standard error of about 0.15% (500: 0.11%;
// 1,000: 0.08%). That is below the optimiser's own run-to-run variation
// (0.1-0.9% on the same team), so more iterations mostly buy precision the
// substats don't have.
var refineLevels = map[string]RefineOptions{
	"light":    {Top: 15, PerElement: 3, Iterations: 250},
	"standard": {Top: 30, PerElement: 5, PerCharacter: 1, Iterations: 250},
	"thorough": {Top: 100, PerElement: 20, PerCharacter: 3, Iterations: 500},
}

// waitLimit is how many seconds of waiting for energy or cooldowns make a
// thorough run's "skip waiting teams" leave a team out.
const waitLimit = 5.0

// pinTeams is how many of each pinned character's best teams are refined, so
// the refined solve can still give every pinned character a team.
const pinTeams = 3

// options returns the settings a snapshot's refined run uses.
func refineOptions(s Snapshot) RefineOptions {
	o := refineLevels[s.Mode]
	if s.Mode == "thorough" && s.Refine != nil {
		o = *s.Refine
	}
	return o
}

// refine is a refined run's second half: results and quick are the quick
// run's, already saved.
func (e *engine) refine(id string, st *Status, snap Snapshot, results []coarse.Result, quick *Result,
	acc *good.Account, rule *optimizer.Rule) error {

	o := refineOptions(snap)
	mode := snap.Artifacts
	if mode == "" {
		mode = "kqm"
	}
	p := pass{sim: &coarse.Optimiser{Bin: e.refineSim, Options: coarse.SubstatModes[mode]}, iterations: o.Iterations, keepStats: true}

	jobs := map[string]coarse.Job{}
	for _, v := range e.candidates(results, quick, o, snap.Pins) {
		if j, ok := e.confirmJob(v, acc, rule, p); ok {
			jobs[confirmKey(v)] = j
		}
	}
	st.State, st.Done, st.Total, st.Cached = "refining", 0, len(jobs), 0
	e.runs.setStatus(id, st)
	first := true
	progress := func(done, total int) {
		if first { // the first report is what the cache already had
			st.Cached, first = done, false
		}
		st.Done, st.Total = done, total
		e.runs.setStatus(id, st)
	}
	var refined []coarse.Result
	for _, r := range e.simulate(jobs, p, progress) {
		refined = append(refined, r)
	}
	log.Printf("run %s: refined %d of %d teams (%s, %d iterations)", id, len(refined), len(jobs), mode, o.Iterations)
	if len(refined) == 0 {
		return fmt.Errorf("no team could be refined")
	}

	st.State = "solving"
	e.runs.setStatus(id, st)
	// Swapped weapons are refined too, so every number shown is refined.
	res, err := e.solveConfirmed(refined, acc, rule, snap.Pins, p, nil)
	if err != nil {
		return err
	}
	res.Mode, res.Teams, res.Refined, res.Artifacts = snap.Mode, quick.Teams, len(refined), mode
	return e.runs.store.write(id, "result.json", res)
}

// confirmKey names a result's rotation with its weapons.
func confirmKey(v coarse.Result) string {
	ws := make([]string, len(v.Weapons))
	for i, w := range v.Weapons {
		ws[i] = optimizer.WeaponKey(w)
	}
	return optimizer.ConfirmKey(v.Source+"/"+v.ID, ws)
}

// candidates are the teams a refined run simulates again, each as a quick
// result (rotation and weapons): the quick run's choices, the best teams by o,
// and each pinned character's best teams.
func (e *engine) candidates(results []coarse.Result, quick *Result, o RefineOptions, pins []string) []coarse.Result {
	byRotation := map[string]coarse.Result{}
	for _, r := range results {
		if r.Error == "" {
			byRotation[r.Source+"/"+r.ID] = r
		}
	}
	var out []coarse.Result
	seen := map[string]bool{}
	add := func(r coarse.Result) {
		if k := confirmKey(r); !seen[k] {
			seen[k] = true
			out = append(out, r)
		}
	}
	waits := func(r coarse.Result) bool {
		total := 0.0
		for _, s := range r.Waits {
			total += s
		}
		return o.SkipWaiting && total > waitLimit
	}

	// The quick run's choices, with the weapons it chose for them.
	for _, bd := range quick.Breakdowns {
		for _, t := range bd.Teams {
			r, ok := byRotation[t.Source+"/"+t.Rotation]
			if !ok || len(t.Members) != len(r.Team) {
				continue
			}
			r.Weapons = make([]string, len(t.Members))
			for i, m := range t.Members {
				r.Weapons[i] = fmt.Sprintf("%s: %s R%d", m.Key, m.Weapon, m.Refinement)
			}
			add(r)
		}
	}
	for _, r := range coarse.Select(results, coarse.Picks{Top: o.Top, PerElement: o.PerElement, PerCharacter: o.PerCharacter}) {
		if !waits(r) {
			add(r)
		}
	}
	if len(pins) > 0 {
		best := coarse.Select(results, coarse.Picks{Top: len(results)})
		for _, pin := range pins {
			n := 0
			for _, r := range best {
				if n < pinTeams && contains(r.Team, pin) {
					add(r)
					n++
				}
			}
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// substatNames are the optimiser's stat keys as the Details panel shows them.
var substatNames = map[string]string{
	"cr": "CRIT Rate", "cd": "CRIT DMG", "atk%": "ATK%", "hp%": "HP%", "def%": "DEF%",
	"em": "EM", "er": "Energy Recharge", "atk": "ATK", "hp": "HP", "def": "DEF",
}

// substats summarises a refined result's chosen substats per member: the
// rolls the optimiser placed in each stat, most first, from the "add stats"
// lines whose values are counted rolls (e.g. cr=0.0331*10). Every stat also
// has a few fixed rolls (2 in KQM standard, 1 in top 1%); those are left out.
func substats(r coarse.Result, cat *catalog.Catalog) map[string]string {
	fixed := 2
	if strings.Contains(r.SimBinary, "fixed_substats_count=1") {
		fixed = 1
	}
	out := map[string]string{}
	for _, l := range r.Stats {
		m := statsLine.FindStringSubmatch(l)
		f := buildLine.FindStringSubmatch(l)
		if m == nil || f == nil || !rollLine.MatchString(m[1]) {
			continue
		}
		type roll struct {
			name string
			n    int
		}
		var rolls []roll
		for _, kv := range rollPart.FindAllStringSubmatch(m[1], -1) {
			var n int
			fmt.Sscan(kv[2], &n)
			if name, ok := substatNames[kv[1]]; ok && n > fixed {
				rolls = append(rolls, roll{name, n - fixed})
			}
		}
		sort.SliceStable(rolls, func(i, j int) bool { return rolls[i].n > rolls[j].n })
		s := ""
		for i, x := range rolls {
			if i == 5 {
				break
			}
			if s != "" {
				s += " · "
			}
			s += fmt.Sprintf("%s %d", x.name, x.n)
		}
		if ch, ok := cat.ResolveCharacter(f[1]); ok { // configs may use aliases, e.g. "arle"
			out[ch.Key] = s
		}
	}
	return out
}
