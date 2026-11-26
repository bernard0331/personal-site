package gear

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
)

//go:embed gear_solve.py
var solveScript []byte

// Wearer is one character to gear in an allocation: its measurements and
// which team it is in.
type Wearer struct {
	Measured
	Team int
	// ExtraER raises every one of its floors, e.g. after the character
	// still waited for energy in a check.
	ExtraER float64
}

// Assignment is what a character was given.
type Assignment struct {
	Char   string
	Team   int
	Pieces []Piece
	Layout Layout
	Sets   map[string]int // the pieces' sets, for the config
	Stats  Stats          // the pieces' stats in total
	Value  float64        // the measured value of pieces and layout over standard artifacts
	ER     float64        // artifact plus set-bonus ER
	// ERShort is how far below its floor the character's ER is, when no
	// allocation from the inventory reaches it (0 when it does).
	ERShort float64
}

// Allocation is the answer for a set of teams.
type Allocation struct {
	Assignments []Assignment
	Optimal     bool
}

// Allocator runs the HiGHS helper.
type Allocator struct {
	Python    string        // a Python with highspy
	TimeLimit time.Duration // per solve (default 1 min)
	// Keep is how many of each slot's best pieces (by measured score) stay
	// candidates for a character, besides its best ER pieces and the best
	// pieces of each set its layouts need (default 12).
	Keep int
}

// Score is a piece's measured value to the character's team.
func (m *Measured) Score(p Piece) float64 {
	s := 0.0
	for k, v := range p.Stats {
		s += m.Weights[k] * v
	}
	return s
}

// layouts are the character's candidate layouts plus its standard sets
// (value 0, the measurements' reference) and no set at all.
func (m *Measured) layouts() []LayoutValue {
	own := Layout{Name: "own"}
	for set, n := range m.OwnSets {
		if n >= 2 {
			own.Need = append(own.Need, SetNeed{set, min(n, 4)})
		}
	}
	out := []LayoutValue{{own, 0, m.ERNeed}}
	for _, l := range m.Layouts {
		if l.ERNeed == 0 {
			l.ERNeed = m.ERNeed // not checked: the character's own floor
		}
		if !math.IsInf(l.ERNeed, 1) {
			out = append(out, l)
		}
	}
	return out
}

// Allocate gives each wearer five pieces from pieces. objective is "sum"
// (the teams' total) or "min" (the weakest team); teamBase is each team's DPS
// on standard artifacts.
func (a *Allocator) Allocate(wearers []Wearer, pieces []Piece, objective string, teamBase []float64) (*Allocation, error) {
	keep := a.Keep
	if keep <= 0 {
		keep = 12
	}
	type cand struct {
		Piece int     `json:"piece"`
		Slot  string  `json:"slot"`
		Score float64 `json:"score"`
		ER    float64 `json:"er"`
		CR    float64 `json:"cr"`
	}
	type need struct {
		Pieces []int `json:"pieces"`
		Count  int   `json:"count"`
	}
	type layout struct {
		Value  float64  `json:"value"`
		ER     float64  `json:"er"`
		Need   []need   `json:"need"`
		ERNeed *float64 `json:"er_need"` // null: no floor
	}
	type char struct {
		Team    int      `json:"team"`
		Cands   []cand   `json:"cands"`
		Layouts []layout `json:"layouts"`
		CRCap   *float64 `json:"cr_cap"`   // most CRIT Rate its artifacts may add; null: none
		ERValue float64  `json:"er_value"` // soft solve: what a point below its floor costs
	}
	in := struct {
		Chars     []char    `json:"chars"`
		TeamBase  []float64 `json:"team_base"`
		Objective string    `json:"objective"`
		TimeLimit float64   `json:"time_limit"`
	}{Objective: objective, TeamBase: teamBase, TimeLimit: 60}
	// The helper solves with hard ER floors first, and only if no
	// allocation meets them all, again with each point missed costing its
	// er_value.
	if a.TimeLimit > 0 {
		in.TimeLimit = a.TimeLimit.Seconds()
	}
	byID := map[int]Piece{}
	for _, p := range pieces {
		byID[p.ID] = p
	}
	lays := make([][]LayoutValue, len(wearers))
	for wi, w := range wearers {
		m := &w.Measured
		lays[wi] = m.layouts()
		chosen := map[int]bool{}
		for _, slot := range Slots {
			var ps []Piece
			for _, p := range pieces {
				if p.Slot == slot {
					ps = append(ps, p)
				}
			}
			if len(ps) == 0 {
				return nil, fmt.Errorf("no %s in the inventory", slot)
			}
			sort.Slice(ps, func(i, j int) bool { return m.Score(ps[i]) > m.Score(ps[j]) })
			for i := 0; i < len(ps) && i < keep; i++ {
				chosen[ps[i].ID] = true
			}
			byER := append([]Piece(nil), ps...)
			sort.Slice(byER, func(i, j int) bool { return byER[i].Stats["enerRech_"] > byER[j].Stats["enerRech_"] })
			for i := 0; i < len(byER) && i < 3 && byER[i].Stats["enerRech_"] > 0; i++ {
				chosen[byER[i].ID] = true
			}
			for _, l := range lays[wi] {
				for _, n := range l.Need {
					k := 0
					for _, p := range ps { // ps is best-first
						if n.Matches(p.Set) {
							chosen[p.ID] = true
							if k++; k == 4 {
								break
							}
						}
					}
				}
			}
		}
		c := char{Team: w.Team, ERValue: m.ERValue}
		if cr := m.critRateOther(); cr > 0 {
			v := math.Max(0, 100-cr)
			c.CRCap = &v
		}
		ids := make([]int, 0, len(chosen))
		for id := range chosen {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		for _, id := range ids {
			p := byID[id]
			c.Cands = append(c.Cands, cand{id, p.Slot, m.Score(p), p.Stats["enerRech_"], p.Stats["critRate_"]})
		}
		for _, l := range lays[wi] {
			out := layout{Value: l.Value, ER: SetER(l.sample())}
			if !math.IsInf(l.ERNeed, 1) {
				v := l.ERNeed + w.ExtraER
				out.ERNeed = &v
			}
			for _, n := range l.Need {
				nd := need{Count: n.Count}
				for _, id := range ids {
					if n.Matches(byID[id].Set) {
						nd.Pieces = append(nd.Pieces, id)
					}
				}
				out.Need = append(out.Need, nd)
			}
			c.Layouts = append(c.Layouts, out)
		}
		in.Chars = append(in.Chars, c)
	}

	body, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "gear-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	script := filepath.Join(dir, "gear_solve.py")
	if err := os.WriteFile(script, solveScript, 0o600); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(in.TimeLimit)*time.Second+time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, a.Python, script)
	cmd.Stdin = bytes.NewReader(body)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gear solver: %v: %s", err, stderr.String())
	}
	var res struct {
		Chars []struct {
			Pieces  []int   `json:"pieces"`
			Layout  int     `json:"layout"`
			ERShort float64 `json:"er_short"`
		} `json:"chars"`
		Optimal    bool   `json:"optimal"`
		Infeasible bool   `json:"infeasible"`
		Status     string `json:"status"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		return nil, fmt.Errorf("gear solver: %v", err)
	}
	if res.Infeasible {
		return nil, fmt.Errorf("no allocation meets every character's Energy Recharge floor and set layout")
	}
	if len(res.Chars) != len(wearers) {
		return nil, fmt.Errorf("gear solver found no answer (%s)", res.Status)
	}
	out := &Allocation{Optimal: res.Optimal}
	for wi, w := range wearers {
		r := res.Chars[wi]
		as := Assignment{Char: w.Char, Team: w.Team, Sets: map[string]int{}, Stats: Stats{}}
		for _, id := range r.Pieces {
			p := byID[id]
			as.Pieces = append(as.Pieces, p)
			as.Sets[p.Set]++
			as.Stats.Add(p.Stats)
			as.Value += w.Score(p)
		}
		sort.Slice(as.Pieces, func(i, j int) bool { return slotIndex(as.Pieces[i].Slot) < slotIndex(as.Pieces[j].Slot) })
		if r.Layout >= 0 && r.Layout < len(lays[wi]) {
			as.Layout = lays[wi][r.Layout].Layout
			as.Value += lays[wi][r.Layout].Value
		}
		as.ER = as.Stats["enerRech_"] + SetER(as.Sets)
		as.ERShort = r.ERShort
		out.Assignments = append(out.Assignments, as)
	}
	return out, nil
}

func slotIndex(s string) int {
	for i, x := range Slots {
		if x == s {
			return i
		}
	}
	return len(Slots)
}

// Choose picks one build per wearer (builds[i] for wearers[i]) so that no
// piece is in two chosen builds, with the highest estimated value (objective
// as in Allocate).
func (a *Allocator) Choose(wearers []Wearer, builds [][]Build, objective string, teamBase []float64) (*Allocation, error) {
	type build struct {
		Pieces []int   `json:"pieces"`
		Value  float64 `json:"value"`
	}
	type char struct {
		Team   int     `json:"team"`
		Builds []build `json:"builds"`
	}
	in := struct {
		Mode      string    `json:"mode"`
		Chars     []char    `json:"chars"`
		TeamBase  []float64 `json:"team_base"`
		Objective string    `json:"objective"`
		TimeLimit float64   `json:"time_limit"`
	}{Mode: "builds", Objective: objective, TeamBase: teamBase, TimeLimit: 60}
	if a.TimeLimit > 0 {
		in.TimeLimit = a.TimeLimit.Seconds()
	}
	for wi, w := range wearers {
		c := char{Team: w.Team}
		for _, b := range builds[wi] {
			ids := make([]int, len(b.Pieces))
			for i, p := range b.Pieces {
				ids[i] = p.ID
			}
			c.Builds = append(c.Builds, build{ids, b.Value})
		}
		in.Chars = append(in.Chars, c)
	}
	var res struct {
		Chosen     []int  `json:"chosen"`
		Optimal    bool   `json:"optimal"`
		Infeasible bool   `json:"infeasible"`
		Status     string `json:"status"`
	}
	if err := a.run(in, &res); err != nil {
		return nil, err
	}
	if res.Infeasible || len(res.Chosen) != len(wearers) {
		return nil, fmt.Errorf("no set of builds shares no piece (%s)", res.Status)
	}
	out := &Allocation{Optimal: res.Optimal}
	for wi, w := range wearers {
		bi := res.Chosen[wi]
		if bi < 0 || bi >= len(builds[wi]) {
			return nil, fmt.Errorf("gear solver chose no build for %s", w.Char)
		}
		b := builds[wi][bi]
		out.Assignments = append(out.Assignments, Assignment{Char: w.Char, Team: w.Team, Pieces: b.Pieces,
			Layout: b.Layout.Layout, Sets: b.Sets, Stats: b.Stats, Value: b.Value, ER: b.ER, ERShort: b.ERShort})
	}
	return out, nil
}

// AsBuild turns an assignment back into a build (for Choose's candidates).
func (as Assignment) AsBuild(m *Measured) Build {
	return Build{Pieces: as.Pieces, Layout: LayoutValue{Layout: as.Layout}, Stats: as.Stats, Sets: as.Sets,
		ER: as.ER, ERShort: as.ERShort, Value: m.Value(as.Stats) - as.ERShort*m.ERValue}
}

// run sends in to the HiGHS helper and reads its answer into out.
func (a *Allocator) run(in any, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "gear-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	script := filepath.Join(dir, "gear_solve.py")
	if err := os.WriteFile(script, solveScript, 0o600); err != nil {
		return err
	}
	limit := a.TimeLimit
	if limit <= 0 {
		limit = time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*limit+time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, a.Python, script)
	cmd.Stdin = bytes.NewReader(body)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gear solver: %v: %s", err, stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
		return fmt.Errorf("gear solver: %v", err)
	}
	return nil
}
