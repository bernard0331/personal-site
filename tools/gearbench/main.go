// Command gearbench gears one team from a player's own artifacts, with the
// measurements and checks simulated on the fan-out workers (Cloud Run), and
// prints what it measured, the pieces it chose and the team's DPS before and
// after. It is step 1 of docs/real-artifacts-plan.md.
//
//	go run ./tools/gearbench -config team.txt -good account.json \
//	  -store gs://BUCKET/fanout -launch service:https://WORKER_URL
//
// -gear names the characters to gear (default: all). The others keep the
// gear the config gives them, and so do their equipped pieces: those are left
// out of the pool.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
	"github.com/bernard0331/personal-site/services/genshin-calc/coarse"
	"github.com/bernard0331/personal-site/services/genshin-calc/fanout"
	"github.com/bernard0331/personal-site/services/genshin-calc/gear"
	"github.com/bernard0331/personal-site/services/genshin-calc/good"
)

func main() {
	cfgPath := flag.String("config", "", "the team's config (its artifacts are the reference)")
	goodPath := flag.String("good", "", "the player's GOOD file")
	gearList := flag.String("gear", "", "characters to gear, comma-separated (default: every one in the config)")
	layoutSpec := flag.String("layouts", "", `layouts to try for every geared character, e.g. "goldentroupe:4,tenacityofthemillelith:4,goldentroupe:2+atk18:2" (default: each character's most common in -db)`)
	db := flag.String("db", "local/rotations/kqm-db.jsonl", "the KQM Sim Database cache, for each character's common sets")
	store := flag.String("store", "", "gs://bucket/prefix for the batches")
	launch := flag.String("launch", "", "service:https://WORKER_URL")
	shards := flag.Int("shards", 16, "workers at once")
	iterations := flag.Int("iterations", 1000, "iterations per stat and layout measurement")
	erIterations := flag.Int("er-iterations", 300, "iterations per ER sweep point")
	checkIterations := flag.Int("check-iterations", 1000, "iterations for the before and after check")
	seed := flag.Int64("seed", 1, "the measurements' seed")
	python := flag.String("python", "local/highs-venv/bin/python", "a Python with highspy")
	rounds := flag.Int("rounds", 3, "measure-allocate rounds, each measured at the previous round's build")
	verbose := flag.Bool("v", false, "print each round's measurements")
	keepBuilds := flag.Int("builds", 60, "best whole builds per character the final choice picks from")
	flag.Parse()

	raw, err := os.ReadFile(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	cfg := string(raw)
	cat, err := catalog.Load()
	if err != nil {
		log.Fatal(err)
	}
	f, err := os.Open(*goodPath)
	if err != nil {
		log.Fatal(err)
	}
	acc, _, err := good.Parse(f, cat)
	f.Close()
	if err != nil {
		log.Fatal(err)
	}

	team := gear.Characters(cfg)
	toGear := team
	if *gearList != "" {
		toGear = strings.Split(*gearList, ",")
	}
	gearedSet := map[string]bool{}
	for _, c := range toGear {
		gearedSet[c] = true
	}
	var pool []gear.Piece
	for _, p := range gear.Pieces(acc.Artifacts) {
		if p.Location != "" && !gearedSet[p.Location] && contains(team, p.Location) {
			continue // a teammate who keeps its gear
		}
		pool = append(pool, p)
	}
	log.Printf("%d pieces in the pool", len(pool))

	var chars []gear.Character
	for _, c := range toGear {
		ch, ok := cat.Character(c)
		if !ok {
			log.Fatalf("unknown character %s", c)
		}
		ls, err := layouts(*layoutSpec, c, *db, pool)
		if err != nil {
			log.Fatal(err)
		}
		chars = append(chars, gear.Character{Key: c, Element: ch.Element, Layouts: ls})
		names := []string{}
		for _, l := range ls {
			names = append(names, l.Name)
		}
		log.Printf("%s (%s): layouts %s", c, ch.Element, strings.Join(names, ", "))
	}

	st, err := fanout.Open(*store)
	if err != nil {
		log.Fatal(err)
	}
	co := &fanout.Coordinator{Store: st, Shards: *shards, Seed: *seed, Timeout: time.Hour,
		Launcher: &fanout.Service{URL: strings.TrimPrefix(*launch, "service:"), Token: &fanout.IDToken{}, Log: log.Printf, Parallel: *shards}}
	run := func(jobs []coarse.Job, seed int64) map[string]coarse.Result {
		co.Seed = seed
		out := map[string]coarse.Result{}
		start := time.Now()
		missing, err := co.Run(context.Background(), jobs, fanout.Label("gear"), fanout.Measure, "", func(r coarse.Result) {
			out[r.ID] = r
		}, nil)
		if err != nil || len(missing) > 0 {
			log.Fatalf("%d of %d simulations missing: %v", len(missing), len(jobs), err)
		}
		log.Printf("%d simulations in %s", len(jobs), time.Since(start).Round(time.Second))
		return out
	}

	// Rounds: measure at the current build, allocate, check. Stat values
	// are only linear near where they were measured (crit rate and crit
	// damage multiply, and crit rate caps at 100%), so each round measures
	// again at the previous round's build. A character that still waits for
	// energy in the check gets a higher floor and the round solves again.
	alloc := &gear.Allocator{Python: *python}
	extra := map[string]float64{} // ER added to a character's floor after it waited
	minWait := map[string]float64{} // the least each character waits at any ER (from the sweeps)
	type result struct {
		name string
		cfg  string
		al   *gear.Allocation
	}
	var results []result
	base := cfg
	for r := 1; r <= *rounds; r++ {
		plan, err := gear.NewPlan(base, chars, *iterations, *erIterations)
		if err != nil {
			log.Fatal(err)
		}
		ms, err := plan.Analyze(run(plan.Jobs, *seed))
		if err != nil {
			log.Fatal(err)
		}
		checks, err := plan.ERChecks(ms, *erIterations)
		if err != nil {
			log.Fatal(err)
		}
		plan.ApplyERChecks(ms, run(checks, *seed))
		for _, m := range ms {
			minWait[m.Char] = m.MinWait
		}
		if *verbose {
			printMeasured(ms, base)
		}
		var al *gear.Allocation
		var geared string
		for fix := 0; ; fix++ {
			ws := make([]gear.Wearer, len(ms))
			for i, m := range ms {
				ws[i] = gear.Wearer{Measured: m, ExtraER: extra[m.Char]}
			}
			next, err := alloc.Allocate(ws, pool, "sum", []float64{ms[0].BaseDPS})
			if err != nil {
				if al == nil {
					log.Fatal(err)
				}
				log.Printf("round %d: no allocation with higher floors (%v); keeping the last one", r, err)
				break
			}
			// Crit-aware builds: each character's best whole builds by the
			// estimate, plus the linear answer (so some choice always fits),
			// then one per character with no piece shared.
			builds := make([][]gear.Build, len(ws))
			for i := range ws {
				builds[i] = append(ws[i].Measured.Builds(pool, ws[i].ExtraER, *keepBuilds), next.Assignments[i].AsBuild(&ws[i].Measured))
			}
			if chosen, err := alloc.Choose(ws, builds, "sum", []float64{ms[0].BaseDPS}); err != nil {
				log.Printf("round %d: build choice failed (%v); using the linear answer", r, err)
			} else {
				next = chosen
			}
			al = next
			geared = cfg
			for _, a := range al.Assignments {
				if geared, err = gear.WithGear(geared, a.Char, a.Sets, a.Stats); err != nil {
					log.Fatal(err)
				}
			}
			chk := run([]coarse.Job{{ID: "check", Team: team, Config: gear.Options(geared, *checkIterations, false)}}, *seed+1_000_000)["check"]
			var waiting []string
			for i, c := range team {
				if geared := gearedSet[c]; geared && i < len(chk.EnergyWaits) && chk.EnergyWaits[i] > minWait[c]+0.5 {
					waiting = append(waiting, fmt.Sprintf("%s %.1fs", c, chk.EnergyWaits[i]))
					extra[c] += 10
				}
			}
			log.Printf("round %d: team DPS %.0f%s", r, chk.DPS, map[bool]string{true: ", waiting: " + strings.Join(waiting, ", "), false: ""}[len(waiting) > 0])
			if len(waiting) == 0 || fix == 2 {
				break
			}
		}
		results = append(results, result{fmt.Sprintf("round %d", r), geared, al})
		base = geared
	}

	// Check every round's answer against the team as it came, on new rolls,
	// and keep the best that never waits for energy.
	jobs := []coarse.Job{{ID: "before", Team: team, Config: gear.Options(cfg, *checkIterations, false)}}
	for _, r := range results {
		jobs = append(jobs, coarse.Job{ID: r.name, Team: team, Config: gear.Options(r.cfg, *checkIterations, false)})
	}
	check := run(jobs, *seed+2_000_000)
	before := check["before"].DPS
	fmt.Printf("\nTeam DPS as it came: %.0f\n", before)
	best := -1
	for i, r := range results {
		c := check[r.name]
		waits := 0.0 // the most any geared character waits beyond its least
		for i, w := range c.EnergyWaits {
			if i < len(team) && gearedSet[team[i]] {
				waits = math.Max(waits, w-minWait[team[i]])
			}
		}
		fmt.Printf("  %s: %.0f (%+.1f%%), energy waiting beyond the least %.1f s\n", r.name, c.DPS, 100*(c.DPS/before-1), waits)
		b := check["before"]
		for ci, ch := range team {
			if ci >= len(c.CharDPS) || ci >= len(b.CharDPS) {
				continue
			}
			was, now := gear.ArtifactStats(cfg, ch), gear.ArtifactStats(r.cfg, ch)
			fmt.Printf("    %-12s DPS %7.0f -> %7.0f | CR %5.1f -> %5.1f  CD %5.1f -> %5.1f  ATK%% %5.1f -> %5.1f  ER %5.1f -> %5.1f  EM %4.0f -> %4.0f  HP%% %5.1f -> %5.1f\n",
				ch, b.CharDPS[ci], c.CharDPS[ci], was["critRate_"], now["critRate_"], was["critDMG_"], now["critDMG_"],
				was["atk_"], now["atk_"], was["enerRech_"], now["enerRech_"], was["eleMas"], now["eleMas"], was["hp_"], now["hp_"])
		}
		if waits <= 0.5 && (best < 0 || c.DPS > check[results[best].name].DPS) {
			best = i
		}
	}
	if best < 0 {
		log.Print("every round's answer waits for energy; showing the best anyway")
		for i, r := range results {
			if best < 0 || check[r.name].DPS > check[results[best].name].DPS {
				best = i
			}
		}
	}
	fmt.Printf("\nBest: %s\n", results[best].name)
	for _, a := range results[best].al.Assignments {
		short := ""
		if a.ERShort > 0 {
			short = fmt.Sprintf(" (%.1f below its floor: the inventory can't reach it)", a.ERShort)
		}
		fmt.Printf("%s: layout %s, ER %.1f%s\n", a.Char, a.Layout.Name, a.ER, short)
		for _, p := range a.Pieces {
			fmt.Printf("  %-8s %-28s %-14s +%-2d (now on %s) %s\n", p.Slot, p.Set, p.Main, p.Level, orNone(p.Location), subs(p))
		}
	}
	os.WriteFile("gearbench-after.txt", []byte(results[best].cfg), 0o644)
}

func printMeasured(ms []gear.Measured, cfg string) {
	for _, m := range ms {
		fmt.Printf("\n%s: ER floor %s (this build gives %.1f)\n", m.Char, pct(m.ERNeed), gear.ArtifactStats(cfg, m.Char)["enerRech_"]+gear.SetER(m.OwnSets))
		keys := make([]string, 0, len(m.Weights))
		for k := range m.Weights {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return m.Weights[keys[i]] > m.Weights[keys[j]] })
		for _, k := range keys {
			if m.Weights[k] != 0 {
				fmt.Printf("  %-14s %8.1f team DPS per point\n", k, m.Weights[k])
			}
		}
		for _, l := range m.Layouts {
			fmt.Printf("  layout %-40s %+8.0f\n", l.Name, l.Value)
		}
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func pct(v float64) string {
	if math.IsInf(v, 1) {
		return "not reachable"
	}
	return fmt.Sprintf("%.1f%%", v)
}

func orNone(s string) string {
	if s == "" {
		return "nobody"
	}
	return s
}

func subs(p gear.Piece) string {
	var out []string
	for k, v := range p.Stats {
		if k != p.Main {
			out = append(out, fmt.Sprintf("%s %.1f", k, v))
		}
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// layouts parses -layouts, or picks the character's most common layouts in
// the database that the pool can make.
func layouts(spec, char, db string, pool []gear.Piece) ([]gear.Layout, error) {
	if spec != "" {
		var out []gear.Layout
		for _, l := range strings.Split(spec, ",") {
			lay := gear.Layout{Name: l}
			for _, part := range strings.Split(l, "+") {
				set, n, ok := strings.Cut(part, ":")
				var count int
				if _, err := fmt.Sscan(n, &count); !ok || err != nil {
					return nil, fmt.Errorf("layout %q: want set:count+set:count", l)
				}
				lay.Need = append(lay.Need, gear.SetNeed{Set: set, Count: count})
			}
			out = append(out, lay)
		}
		return out, nil
	}
	have := map[string]map[string]bool{} // set -> slots in the pool
	for _, p := range pool {
		if have[p.Set] == nil {
			have[p.Set] = map[string]bool{}
		}
		have[p.Set][p.Slot] = true
	}
	four, two := map[string]int{}, map[string]int{}
	re := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(char) + `[ \t]+add[ \t]+set[ \t]*=[ \t]*"([a-z0-9]+)"[ \t]+count[ \t]*=[ \t]*(\d)`)
	f, err := os.Open(db)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var row struct{ Config string }
		if json.Unmarshal(sc.Bytes(), &row) != nil {
			continue
		}
		for _, m := range re.FindAllStringSubmatch(row.Config, -1) {
			if m[2] == "4" {
				four[m[1]]++
			} else {
				two[m[1]]++
			}
		}
	}
	top := func(c map[string]int, minSlots, n int) []string {
		var sets []string
		for s := range c {
			if len(have[s]) >= minSlots {
				sets = append(sets, s)
			}
		}
		sort.Slice(sets, func(i, j int) bool { return c[sets[i]] > c[sets[j]] || c[sets[i]] == c[sets[j]] && sets[i] < sets[j] })
		if len(sets) > n {
			sets = sets[:n]
		}
		return sets
	}
	var out []gear.Layout
	for _, s := range top(four, 4, 4) {
		out = append(out, gear.Layout{Name: s + ":4", Need: []gear.SetNeed{{Set: s, Count: 4}}})
	}
	twos := top(two, 2, 3)
	for i := 0; i < len(twos); i++ {
		for j := i + 1; j < len(twos); j++ {
			out = append(out, gear.Layout{Name: twos[i] + ":2+" + twos[j] + ":2",
				Need: []gear.SetNeed{{Set: twos[i], Count: 2}, {Set: twos[j], Count: 2}}})
		}
	}
	return out, nil
}
