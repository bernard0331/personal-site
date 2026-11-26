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
	geared := map[string]bool{}
	for _, c := range toGear {
		geared[c] = true
	}
	var pool []gear.Piece
	for _, p := range gear.Pieces(acc.Artifacts) {
		if p.Location != "" && !geared[p.Location] && contains(team, p.Location) {
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

	// 1. Measure.
	plan, err := gear.NewPlan(cfg, chars, *iterations, *erIterations)
	if err != nil {
		log.Fatal(err)
	}
	ms, err := plan.Analyze(run(plan.Jobs, *seed))
	if err != nil {
		log.Fatal(err)
	}
	for _, m := range ms {
		fmt.Printf("\n%s: ER floor %s (standard artifacts give %.1f)\n", m.Char, pct(m.ERNeed), gear.ArtifactStats(cfg, m.Char)["enerRech_"]+gear.SetER(m.OwnSets))
		keys := make([]string, 0, len(m.Weights))
		for k := range m.Weights {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return m.Weights[keys[i]] > m.Weights[keys[j]] })
		for _, k := range keys {
			fmt.Printf("  %-14s %8.1f team DPS per point\n", k, m.Weights[k])
		}
		for _, l := range m.Layouts {
			fmt.Printf("  layout %-40s %+8.0f\n", l.Name, l.Value)
		}
	}

	// 2. Allocate.
	ws := make([]gear.Wearer, len(ms))
	for i, m := range ms {
		ws[i] = gear.Wearer{Measured: m}
	}
	al, err := (&gear.Allocator{Python: *python}).Allocate(ws, pool, "sum", []float64{ms[0].BaseDPS})
	if err != nil {
		log.Fatal(err)
	}
	geardCfg := cfg
	fmt.Printf("\nAllocation (optimal: %v)\n", al.Optimal)
	for _, a := range al.Assignments {
		fmt.Printf("%s: layout %s, ER %.1f, measured value %+.0f\n", a.Char, a.Layout.Name, a.ER, a.Value)
		for _, p := range a.Pieces {
			fmt.Printf("  %-8s %-28s %-14s +%-2d (now on %s) %s\n", p.Slot, p.Set, p.Main, p.Level, orNone(p.Location), subs(p))
		}
		if geardCfg, err = gear.WithGear(geardCfg, a.Char, a.Sets, a.Stats); err != nil {
			log.Fatal(err)
		}
	}

	// 3. Check: the team before and after, energy on, same rolls.
	check := run([]coarse.Job{
		{ID: "before", Team: team, Config: gear.Options(cfg, *checkIterations, false)},
		{ID: "after", Team: team, Config: gear.Options(geardCfg, *checkIterations, false)},
	}, *seed+1_000_000)
	b, a := check["before"], check["after"]
	fmt.Printf("\nTeam DPS before %.0f, after %.0f (%+.1f%%)\n", b.DPS, a.DPS, 100*(a.DPS/b.DPS-1))
	for i, c := range team {
		if i < len(a.EnergyWaits) && a.EnergyWaits[i] > 0.5 {
			fmt.Printf("  %s waits %.1f s for energy after\n", c, a.EnergyWaits[i])
		}
	}
	os.WriteFile("gearbench-after.txt", []byte(geardCfg), 0o644)
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
