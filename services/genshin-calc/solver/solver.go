// Package solver chooses a player's best set of teams.
//
// Given simulated teams, it picks teams so that no character, and no weapon
// copy, is used twice (weapons the player has unlimited copies of excepted).
// Two breakdowns: one team per element, or any N teams. Each is ranked two
// ways: the best weakest team (maximise the minimum DPS), or the highest
// average (maximise the sum). The search is exact: branch and bound over
// teams sorted by DPS, with bounds that never cut off a better answer.
package solver

import (
	"sort"
)

// Team is one candidate team.
type Team struct {
	ID      string   // the rotation it was simulated with
	Members []string // character keys
	Weapons []string // weapon keys, one per member
	Element string   // main element
	DPS     float64
}

// Objective is what a set of teams is ranked by.
type Objective int

const (
	Sum Objective = iota // highest average DPS
	Min                  // best weakest team
)

func (o Objective) String() string {
	if o == Min {
		return "best weakest team"
	}
	return "highest average"
}

// Problem is a set of candidate teams and the player's weapon copies.
type Problem struct {
	Teams []Team
	// Copies is how many copies of each weapon the player has; a weapon
	// missing from the map, or with a negative count, is unlimited.
	Copies map[string]int
}

// usage keeps track of what the chosen teams use.
type usage struct {
	chars   map[string]bool
	weapons map[string]int
	copies  map[string]int
}

func newUsage(copies map[string]int) *usage {
	return &usage{chars: map[string]bool{}, weapons: map[string]int{}, copies: copies}
}

func (u *usage) fits(t *Team) bool {
	for _, c := range t.Members {
		if u.chars[c] {
			return false
		}
	}
	need := map[string]int{}
	for _, w := range t.Weapons {
		need[w]++
	}
	for w, n := range need {
		if c, ok := u.copies[w]; ok && c >= 0 && u.weapons[w]+n > c {
			return false
		}
	}
	return true
}

func (u *usage) add(t *Team, sign int) {
	for _, c := range t.Members {
		u.chars[c] = sign > 0
	}
	for _, w := range t.Weapons {
		u.weapons[w] += sign
	}
}

// score of a chosen set under an objective.
func score(o Objective, dps []float64) float64 {
	if len(dps) == 0 {
		return 0
	}
	if o == Min {
		m := dps[0]
		for _, d := range dps[1:] {
			m = min(m, d)
		}
		return m
	}
	s := 0.0
	for _, d := range dps {
		s += d
	}
	return s
}

// Best picks n teams maximising the objective, ignoring elements. It returns
// fewer than n only if no n teams fit together.
func (p *Problem) Best(n int, o Objective) []Team {
	teams := sorted(p.Teams)
	// Fewer teams than asked are only better than none: first find the
	// largest count that fits, then optimise at that count.
	for k := n; k > 0; k-- {
		if best := p.search(teams, k, o); best != nil {
			return best
		}
	}
	return nil
}

func sorted(ts []Team) []Team {
	out := append([]Team(nil), ts...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].DPS > out[j].DPS })
	return out
}

// search finds exactly k fitting teams with the best objective, or nil.
func (p *Problem) search(teams []Team, k int, o Objective) []Team {
	u := newUsage(p.Copies)
	var chosen []int
	var best []int
	bestScore := -1.0
	var dfs func(i int, sum float64)
	dfs = func(i int, sum float64) {
		need := k - len(chosen)
		if need == 0 {
			var s float64
			if o == Min {
				s = teams[chosen[len(chosen)-1]].DPS // sorted: the last is the weakest
			} else {
				s = sum
			}
			if s > bestScore {
				bestScore, best = s, append([]int(nil), chosen...)
			}
			return
		}
		if len(teams)-i < need {
			return
		}
		// Bound: the next `need` teams are the best that could still be added.
		if o == Min {
			if teams[i+need-1].DPS <= bestScore {
				return
			}
		} else {
			b := sum
			for j := i; j < i+need; j++ {
				b += teams[j].DPS
			}
			if b <= bestScore {
				return
			}
		}
		if u.fits(&teams[i]) {
			u.add(&teams[i], 1)
			chosen = append(chosen, i)
			dfs(i+1, sum+teams[i].DPS)
			chosen = chosen[:len(chosen)-1]
			u.add(&teams[i], -1)
		}
		dfs(i+1, sum)
	}
	dfs(0, 0)
	if best == nil {
		return nil
	}
	out := make([]Team, len(best))
	for i, j := range best {
		out[i] = teams[j]
	}
	return out
}

// ByElement picks one team for each of the given elements, maximising the
// objective. If not every element can have a team, it covers as many
// elements as possible first. The result is in the order of elements; an
// element without a team has a zero Team.
func (p *Problem) ByElement(elements []string, o Objective) []Team {
	groups := make([][]Team, len(elements))
	for i, e := range elements {
		for _, t := range p.Teams {
			if t.Element == e {
				groups[i] = append(groups[i], t)
			}
		}
		groups[i] = sorted(groups[i])
	}
	// Search the elements with the fewest teams first: they constrain most.
	order := make([]int, len(elements))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return len(groups[order[a]]) < len(groups[order[b]]) })

	u := newUsage(p.Copies)
	pick := make([]int, len(elements)) // index into the group, or -1
	best := make([]int, len(elements))
	for i := range pick {
		pick[i], best[i] = -1, -1
	}
	bestCovered, bestScore := -1, -1.0
	var dfs func(d, covered int, sum, low float64)
	dfs = func(d, covered int, sum, low float64) {
		if d == len(order) {
			s := sum
			if o == Min {
				s = low
			}
			if covered > bestCovered || (covered == bestCovered && s > bestScore) {
				bestCovered, bestScore = covered, s
				copy(best, pick)
			}
			return
		}
		// Bound: every remaining element gets its best team.
		maxCovered, bSum, bLow := covered, sum, low
		for _, e := range order[d:] {
			if len(groups[e]) > 0 {
				maxCovered++
				bSum += groups[e][0].DPS
				bLow = min(bLow, groups[e][0].DPS)
			}
		}
		bound := bSum
		if o == Min {
			bound = bLow
		}
		if maxCovered < bestCovered || (maxCovered == bestCovered && bound <= bestScore) {
			return
		}
		e := order[d]
		for j := range groups[e] {
			t := &groups[e][j]
			if !u.fits(t) {
				continue
			}
			u.add(t, 1)
			pick[e] = j
			dfs(d+1, covered+1, sum+t.DPS, min(low, t.DPS))
			u.add(t, -1)
		}
		pick[e] = -1
		dfs(d+1, covered, sum, low)
	}
	inf := 1e18
	dfs(0, 0, 0, inf)
	out := make([]Team, len(elements))
	for i, j := range best {
		if j >= 0 {
			out[i] = groups[i][j]
		}
	}
	return out
}
