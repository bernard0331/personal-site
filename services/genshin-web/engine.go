package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
	"github.com/bernard0331/personal-site/services/genshin-calc/coarse"
	"github.com/bernard0331/personal-site/services/genshin-calc/good"
	"github.com/bernard0331/personal-site/services/genshin-calc/optimizer"
	"github.com/bernard0331/personal-site/services/genshin-calc/solver"
	"github.com/bernard0331/personal-site/services/genshin-calc/weapons"
)

// engine works through the run queue, one run at a time (each run already
// uses every core it is given). A quick run simulates every rotation the
// account can field at a low iteration count, then lets the solver choose
// teams for all four breakdowns.
type engine struct {
	runs       *runs
	cat        *catalog.Catalog
	sources    []optimizer.Source // every rotation: the database cache and our own files
	ranking    weapons.Ranking    // weapon trials' ranking; nil means stand-in weapons
	sim        *coarse.Binary
	workers    int
	iterations int
	variants   int           // weapon alternatives per team for the solver
	highs      *solver.HiGHS // nil: the slower branch and bound

	// The shared cache: every simulation any run has done, by key (the
	// config and the simulator build). The same team with the same
	// constellations, weapons and energy is never simulated twice.
	mu        sync.Mutex
	cache     map[string]coarse.Result
	cacheFile *os.File
	cacheW    *bufio.Writer
}

// newEngine loads everything a run needs once, at start-up.
func newEngine(cat *catalog.Catalog, dataDir, dbPath, ownDir, simPath, rankingPath, highsPython string, cpus, iterations, queueSize int) (*engine, error) {
	sources, err := optimizer.ReadRotations(dbPath, ownDir)
	if err != nil {
		return nil, fmt.Errorf("rotations: %w", err)
	}
	sim, err := coarse.NewBinary(simPath)
	if err != nil {
		return nil, fmt.Errorf("simulator: %w", err)
	}
	// One core per simulation, as many at once as there are cores; the same
	// time and memory limits as the command-line passes.
	sim.Threads, sim.Timeout, sim.MaxMemoryMB = 1, 2*time.Minute, 400
	e := &engine{runs: newRuns(cat, dataDir, queueSize), cat: cat, sources: sources, sim: sim, workers: cpus, iterations: iterations}
	if r, err := weapons.ReadRanking(rankingPath); err == nil {
		e.ranking = r
	} else {
		log.Printf("no weapon ranking (%v): using stand-in weapons", err)
	}
	if _, err := os.Stat(highsPython); err == nil {
		e.highs = &solver.HiGHS{Python: highsPython, TimeLimit: 2 * time.Minute}
	} else {
		log.Printf("no HiGHS Python at %s: using the branch and bound", highsPython)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	if err := e.openCache(dataDir + "/cache.jsonl"); err != nil {
		return nil, err
	}
	log.Printf("%d rotations, %d cores, %d iterations per quick run", len(sources), cpus, iterations)
	return e, nil
}

func (e *engine) openCache(path string) error {
	results, err := coarse.ReadResults(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	e.cache = map[string]coarse.Result{}
	for _, r := range results {
		if r.Error == "" || r.Final {
			e.cache[r.Key] = r
		}
	}
	e.cacheFile, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	e.cacheW = bufio.NewWriter(e.cacheFile)
	log.Printf("shared cache: %d simulations", len(e.cache))
	return nil
}

func (e *engine) remember(r coarse.Result) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cache[r.Key] = r
	if b, err := json.Marshal(r); err == nil {
		e.cacheW.Write(append(b, '\n'))
		e.cacheW.Flush()
	}
}

func (e *engine) loop() {
	for {
		id := e.runs.next()
		start := time.Now()
		err := e.execute(id)
		st, _ := e.runs.statusOf(id)
		st.Finished = time.Now().UTC()
		if err != nil {
			log.Printf("run %s failed after %s: %v", id, time.Since(start).Round(time.Second), err)
			st.State, st.Error = "failed", err.Error()
		} else {
			log.Printf("run %s done in %s (%d simulations, %d cached)", id, time.Since(start).Round(time.Second), st.Total, st.Cached)
			st.State = "done"
		}
		e.runs.setStatus(id, &st)
	}
}

// account turns a checked snapshot into the optimizer's account: every
// character level 90, left-out characters removed, and the unlimited weapons
// added when the visitor asked for them.
func (e *engine) account(s Snapshot) *good.Account {
	excluded := map[string]bool{}
	for _, k := range s.Excluded {
		excluded[k] = true
	}
	acc := &good.Account{Source: "team optimizer snapshot"}
	keys := make([]string, 0, len(s.Characters))
	for k := range s.Characters {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !excluded[k] {
			acc.Characters = append(acc.Characters, good.Character{Key: k, Constellation: s.Characters[k], Level: 90})
		}
	}
	wkeys := make([]string, 0, len(s.Weapons))
	for k := range s.Weapons {
		wkeys = append(wkeys, k)
	}
	sort.Strings(wkeys)
	for _, k := range wkeys {
		for _, r := range s.Weapons[k] {
			acc.Weapons = append(acc.Weapons, good.Weapon{Key: k, Refinement: r})
		}
	}
	if s.Unlimited {
		acc.AssumeThreeStars(e.cat)
	}
	return acc
}

func (e *engine) execute(id string) error {
	var snap Snapshot
	if err := e.runs.store.read(id, "snapshot.json", &snap); err != nil {
		return fmt.Errorf("reading the snapshot: %w", err)
	}
	st, _ := e.runs.statusOf(id)
	st.State = "running"
	e.runs.setStatus(id, &st)

	acc := e.account(snap)
	rule := optimizer.NewRule(acc, e.ranking)
	rule.Energy = float64(snap.Energy)
	jobs, _ := optimizer.MakeJobs(e.sources, acc, e.cat, rule, e.iterations)
	if len(jobs) == 0 {
		return errors.New("no team in the database can be built from this account")
	}

	// Results already in the shared cache cost nothing; the rest are simulated.
	var results []coarse.Result
	var todo []coarse.Job
	seen := map[string]bool{}
	e.mu.Lock()
	for _, j := range jobs {
		k := coarse.Key(j.Config, e.sim.Fingerprint())
		if seen[k] {
			continue
		}
		seen[k] = true
		if r, ok := e.cache[k]; ok {
			r.ID, r.Source, r.Team, r.Weapons = j.ID, j.Source, j.Team, j.Weapons
			r.RotCons, r.PlayerCons = j.RotCons, j.PlayerCons
			results = append(results, r)
		} else {
			todo = append(todo, j)
		}
	}
	e.mu.Unlock()
	st.Total, st.Done, st.Cached = len(results)+len(todo), len(results), len(results)
	e.runs.setStatus(id, &st)

	var resMu sync.Mutex
	save := func(r coarse.Result) error {
		e.remember(r)
		resMu.Lock()
		results = append(results, r)
		resMu.Unlock()
		return nil
	}
	progress := func(p coarse.Progress) {
		if p.Done%20 == 0 || p.Done == p.Total {
			st.Done = st.Cached + p.Done
			e.runs.setStatus(id, &st)
		}
	}
	if _, err := coarse.Run(context.Background(), todo, e.sim, e.workers, nil, save, progress); err != nil {
		return err
	}

	st.State = "solving"
	e.runs.setStatus(id, &st)
	result, err := e.solve(results, rule, snap.Pins)
	if err != nil {
		return err
	}
	return e.runs.store.write(id, "result.json", result)
}

// Breakdown is one way of choosing teams, as the results page shows it.
type Breakdown struct {
	Name    string     `json:"name"`    // "element" (7, one per element) or "any" (10)
	Ranking string     `json:"ranking"` // "min" (best weakest team) or "sum" (highest average)
	Optimal bool       `json:"optimal"` // proven best
	Teams   []TeamView `json:"teams"`
	Error   string     `json:"error,omitempty"`
}

// TeamView is one chosen team.
type TeamView struct {
	Element   string       `json:"element"`
	DPS       float64      `json:"dps"`
	SD        float64      `json:"sd"`
	Members   []MemberView `json:"members"`
	Rotation  string       `json:"rotation"` // rotation id
	Source    string       `json:"source"`   // "kqm-db" or "own"
	Flags     []string     `json:"flags,omitempty"`
	Estimated bool         `json:"estimated,omitempty"` // weapons changed after simulating
}

// MemberView is one team member.
type MemberView struct {
	Key        string  `json:"key"`
	Cons       int     `json:"cons"`
	Weapon     string  `json:"weapon"`
	Refinement int     `json:"refinement"`
	DPS        float64 `json:"dps,omitempty"` // missing when the team's DPS is estimated
}

type Result struct {
	Breakdowns []Breakdown `json:"breakdowns"`
	Teams      int         `json:"teams"` // distinct teams the account can field
}

func (e *engine) solve(results []coarse.Result, rule *optimizer.Rule, pins []string) (*Result, error) {
	p, byID := optimizer.Problem(results, optimizer.Alternatives{Rule: rule, Within: 0.10, Max: max(1, e.variants)})
	p.Require = pins
	out := &Result{Teams: len(coarse.BestPerTeam(results))}
	type job struct {
		name string
		obj  solver.Objective
	}
	jobs := []job{{"element", solver.Min}, {"element", solver.Sum}, {"any", solver.Min}, {"any", solver.Sum}}
	out.Breakdowns = make([]Breakdown, len(jobs))
	errs := make([]error, len(jobs))
	solve := func(i int, b job) {
		bd := Breakdown{Name: b.name, Ranking: map[solver.Objective]string{solver.Min: "min", solver.Sum: "sum"}[b.obj]}
		var teams []solver.Team
		if e.highs != nil {
			var r solver.Result
			var err error
			if b.name == "element" {
				r, err = p.ByElementHiGHS(e.highs, optimizer.Elements, b.obj)
			} else {
				r, err = p.BestHiGHS(e.highs, 10, b.obj)
			}
			if errors.Is(err, solver.ErrInfeasible) {
				bd.Error = err.Error()
			} else if err != nil {
				errs[i] = err
				return
			}
			teams, bd.Optimal = r.Teams, r.Optimal
		} else {
			p.Deadline = time.Now().Add(30 * time.Second)
			if b.name == "element" {
				teams = p.ByElement(optimizer.Elements, b.obj)
			} else {
				teams = p.Best(10, b.obj)
			}
			bd.Optimal = !p.Exhausted
		}
		for _, t := range teams {
			if t.ID != "" {
				bd.Teams = append(bd.Teams, teamView(byID[t.ID], t))
			}
		}
		sort.Slice(bd.Teams, func(i, j int) bool { return bd.Teams[i].DPS > bd.Teams[j].DPS })
		out.Breakdowns[i] = bd
	}
	if e.highs != nil {
		// Each HiGHS solve is its own process and only reads the problem, so
		// the four breakdowns are solved at the same time.
		var wg sync.WaitGroup
		for i, b := range jobs {
			wg.Add(1)
			go func() { defer wg.Done(); solve(i, b) }()
		}
		wg.Wait()
	} else {
		for i, b := range jobs { // the branch and bound keeps state on the problem
			solve(i, b)
		}
	}
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func teamView(r coarse.Result, t solver.Team) TeamView {
	v := TeamView{Element: t.Element, DPS: r.DPS, SD: r.DPSSD, Rotation: r.ID, Source: r.Source, Flags: r.Flags,
		Estimated: strings.Contains(t.ID, "~")}
	for i, k := range r.Team {
		m := MemberView{Key: k}
		if i < len(r.PlayerCons) {
			m.Cons = r.PlayerCons[i]
		}
		if i < len(r.Weapons) {
			_, rest, _ := strings.Cut(r.Weapons[i], ": ")
			w, ref, _ := strings.Cut(rest, " R")
			m.Weapon = w
			m.Refinement, _ = strconv.Atoi(ref)
		}
		if i < len(r.CharDPS) {
			m.DPS = r.CharDPS[i]
		}
		v.Members = append(v.Members, m)
	}
	return v
}
