package gear

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// Crit-aware estimates and whole builds.
//
// A straight-line value per stat point flips between extremes when crit is
// involved (2026-11-26, Arlecchino: 70% CRIT Rate and 90% CRIT DMG one round,
// 28% and 188% the next), because CRIT Rate and CRIT DMG multiply and CRIT
// Rate stops at 100%. So a character's value is estimated as
//
//	sum of w_k * stat_k for every stat but crit, plus
//	A * min(1, CRIT Rate) * CRIT DMG     (totals, artifacts and everything else)
//
// with A taken from the measured values of CRIT Rate and CRIT DMG at the
// build they were measured at. Builds (five pieces and a layout) are then
// enumerated per character from each slot's strongest candidates, and the
// solver picks one build per character with no piece used twice.

// critCoef is A: the crit term's size, from the measured crit values.
func (m *Measured) critCoef() float64 {
	cr0 := m.CritRateOther + m.BaseArtifacts["critRate_"]
	cd0 := m.CritDMGOther + m.BaseArtifacts["critDMG_"]
	var est []float64
	// d/dCR of A*cr*cd (cr, cd as fractions, per GOOD point) = A*cd/1e4
	if cd0 > 0 && cr0 < 100 && m.Weights["critRate_"] > 0 {
		est = append(est, m.Weights["critRate_"]*1e4/cd0)
	}
	// d/dCD = A*min(1, cr)/100
	if cr0 > 0 && m.Weights["critDMG_"] > 0 {
		est = append(est, m.Weights["critDMG_"]*100/math.Min(1, cr0/100))
	}
	if len(est) == 0 {
		return 0
	}
	s := 0.0
	for _, e := range est {
		s += e
	}
	return s / float64(len(est))
}

// Value estimates what artifact stats s are worth to the character's team
// (relative: only differences between builds mean anything).
func (m *Measured) Value(s Stats) float64 {
	v := 0.0
	for k, x := range s {
		if k != "critRate_" && k != "critDMG_" {
			v += m.Weights[k] * x
		}
	}
	a := m.critCoef()
	if a == 0 {
		return v + m.Weights["critRate_"]*s["critRate_"] + m.Weights["critDMG_"]*s["critDMG_"]
	}
	cr := math.Min(1, (m.CritRateOther+s["critRate_"])/100)
	cd := (m.CritDMGOther + s["critDMG_"]) / 100
	return v + a*cr*cd
}

// Build is five pieces and the layout they make for one character.
type Build struct {
	Pieces  []Piece
	Layout  LayoutValue
	Stats   Stats
	Sets    map[string]int
	ER      float64 // artifact plus set-bonus ER
	ERShort float64 // below the layout's floor (only when no build meets a floor)
	Value   float64 // estimated value plus the layout's measured value
}

func (b *Build) key() string {
	ids := make([]int, len(b.Pieces))
	for i, p := range b.Pieces {
		ids[i] = p.ID
	}
	sort.Ints(ids)
	var sb strings.Builder
	for _, id := range ids {
		sb.WriteString(strconv.Itoa(id))
		sb.WriteByte(',')
	}
	return sb.String()
}

// perSlot is how many candidates per slot a build enumeration tries.
const perSlot = 8

// Builds enumerates the character's best builds: up to keep of them, all
// meeting their layout's ER floor (raised by extraER) when any build can;
// otherwise the best with each point missed costing ERValue.
func (m *Measured) Builds(pieces []Piece, extraER float64, keep int) []Build {
	bySlot := map[string][]Piece{}
	for _, p := range pieces {
		bySlot[p.Slot] = append(bySlot[p.Slot], p)
	}
	var meet, miss []Build
	seen := map[string]bool{}
	for _, l := range m.layouts() {
		need := l.ERNeed + extraER
		for _, assign := range assignments(l.Need) {
			cands := make([][]Piece, len(Slots))
			ok := true
			for si, slot := range Slots {
				cands[si] = m.candidates(bySlot[slot], assign[si])
				if len(cands[si]) == 0 {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			cur := make([]Piece, len(Slots))
			var walk func(int)
			walk = func(si int) {
				if si == len(Slots) {
					b := Build{Pieces: append([]Piece(nil), cur...), Layout: l, Stats: Stats{}, Sets: map[string]int{}}
					for _, p := range cur {
						b.Stats.Add(p.Stats)
						b.Sets[p.Set]++
					}
					b.ER = b.Stats["enerRech_"] + SetER(b.Sets)
					b.Value = m.Value(b.Stats) + l.Value
					if math.IsInf(need, 1) || b.ER >= need {
						meet = append(meet, b)
					} else {
						b.ERShort = need - b.ER
						b.Value -= b.ERShort * m.ERValue
						miss = append(miss, b)
					}
					return
				}
				for _, p := range cands[si] {
					cur[si] = p
					walk(si + 1)
				}
			}
			walk(0)
			// Keep memory in check: prune to the best few thousand as we go.
			meet = best(meet, 4*keep, seen)
			miss = best(miss, 4*keep, seen)
		}
	}
	if len(meet) > 0 {
		return best(meet, keep, map[string]bool{})
	}
	return best(miss, keep, map[string]bool{})
}

// best keeps the n highest-valued distinct builds.
func best(bs []Build, n int, _ map[string]bool) []Build {
	sort.Slice(bs, func(i, j int) bool { return bs[i].Value > bs[j].Value })
	out := bs[:0]
	dup := map[string]bool{}
	for _, b := range bs {
		k := b.key()
		if dup[k] {
			continue
		}
		dup[k] = true
		out = append(out, b)
		if len(out) == n {
			break
		}
	}
	return out
}

// candidates are a slot's pieces worth trying for one requirement: the
// strongest by the character's values at its measured build, plus the best
// for CRIT Rate, CRIT DMG and ER, so balanced and lopsided builds both appear.
func (m *Measured) candidates(ps []Piece, req *SetNeed) []Piece {
	var pool []Piece
	for _, p := range ps {
		if req == nil || req.Matches(p.Set) {
			pool = append(pool, p)
		}
	}
	pick := map[int]bool{}
	var out []Piece
	take := func(by func(Piece) float64, n int) {
		s := append([]Piece(nil), pool...)
		sort.Slice(s, func(i, j int) bool { return by(s[i]) > by(s[j]) })
		for i := 0; i < len(s) && n > 0; i++ {
			if !pick[s[i].ID] && by(s[i]) > 0 {
				pick[s[i].ID] = true
				out = append(out, s[i])
				n--
			}
		}
	}
	take(func(p Piece) float64 { return m.Value(mergeStats(m.BaseArtifacts, p.Stats)) - m.Value(m.BaseArtifacts) }, perSlot-3)
	take(func(p Piece) float64 { return p.Stats["critRate_"] }, 1)
	take(func(p Piece) float64 { return p.Stats["critDMG_"] }, 1)
	take(func(p Piece) float64 { return p.Stats["enerRech_"] }, 1)
	if len(out) == 0 && len(pool) > 0 {
		out = append(out, pool[0])
	}
	return out
}

func mergeStats(a, b Stats) Stats {
	out := Stats{}
	out.Add(a)
	out.Add(b)
	return out
}

// assignments lists the ways a layout's set needs can sit on the five slots:
// for each slot, the need its piece must meet, or nil for any piece.
func assignments(needs []SetNeed) [][]*SetNeed {
	var labels []*SetNeed
	for i := range needs {
		for j := 0; j < needs[i].Count; j++ {
			labels = append(labels, &needs[i])
		}
	}
	for len(labels) < len(Slots) {
		labels = append(labels, nil)
	}
	if len(labels) > len(Slots) {
		return nil
	}
	var out [][]*SetNeed
	seen := map[string]bool{}
	var perm func([]*SetNeed, int)
	perm = func(a []*SetNeed, k int) {
		if k == len(a) {
			var sb strings.Builder
			for _, n := range a {
				if n == nil {
					sb.WriteString("-|")
				} else {
					sb.WriteString(n.Set + "|")
				}
			}
			if !seen[sb.String()] {
				seen[sb.String()] = true
				out = append(out, append([]*SetNeed(nil), a...))
			}
			return
		}
		for i := k; i < len(a); i++ {
			a[k], a[i] = a[i], a[k]
			perm(a, k+1)
			a[k], a[i] = a[i], a[k]
		}
	}
	perm(labels, 0)
	return out
}
