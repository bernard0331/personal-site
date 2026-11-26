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
	out := append([]LayoutValue{{own, 0}}, m.Layouts...)
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
	}
	type need struct {
		Pieces []int `json:"pieces"`
		Count  int   `json:"count"`
	}
	type layout struct {
		Value float64 `json:"value"`
		ER    float64 `json:"er"`
		Need  []need  `json:"need"`
	}
	type char struct {
		Team    int      `json:"team"`
		Cands   []cand   `json:"cands"`
		Layouts []layout `json:"layouts"`
		ERNeed  *float64 `json:"er_need"`
	}
	in := struct {
		Chars     []char    `json:"chars"`
		TeamBase  []float64 `json:"team_base"`
		Objective string    `json:"objective"`
		TimeLimit float64   `json:"time_limit"`
	}{Objective: objective, TeamBase: teamBase, TimeLimit: 60}
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
		c := char{Team: w.Team}
		ids := make([]int, 0, len(chosen))
		for id := range chosen {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		for _, id := range ids {
			p := byID[id]
			c.Cands = append(c.Cands, cand{id, p.Slot, m.Score(p), p.Stats["enerRech_"]})
		}
		for _, l := range lays[wi] {
			out := layout{Value: l.Value, ER: SetER(l.sample())}
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
		if !math.IsInf(m.ERNeed, 1) {
			v := m.ERNeed
			c.ERNeed = &v
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
			Pieces []int `json:"pieces"`
			Layout int   `json:"layout"`
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
