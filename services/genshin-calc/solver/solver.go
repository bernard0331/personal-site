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
	"math"
	"sort"
	"strings"
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
	// MaxSteps bounds each search (0: no bound). A search that reaches it
	// returns the best answer found so far and sets Exhausted.
	MaxSteps  int
	Exhausted bool
	steps     int
}

// step counts one search step and reports whether the budget is spent.
func (p *Problem) step() bool {
	p.steps++
	if p.MaxSteps > 0 && p.steps > p.MaxSteps {
		p.Exhausted = true
		return true
	}
	return false
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
// fewer than n only if no n teams fit together. For the best weakest team,
// ties are broken by the highest average.
func (p *Problem) Best(n int, o Objective) []Team {
	teams := sorted(p.Teams)
	p.steps, p.Exhausted = 0, false
	for k := n; k > 0; k-- { // fewer teams only if n cannot fit together
		var best []Team
		if o == Min {
			best = p.bestMin(teams, k)
		} else {
			best = p.bestSum(teams, k)
		}
		if best != nil || p.Exhausted {
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

// bestMin: the weakest team of the answer is teams[L-1] for the smallest L
// such that k of teams[:L] fit together. Feasibility only grows with L, so L
// is found by binary search, starting from the highest-average answer (which
// fits, so its weakest team bounds L). Then the best average among teams[:L].
// If the step budget runs out, the best answer known so far is returned.
func (p *Problem) bestMin(teams []Team, k int) []Team {
	known := p.bestSum(teams, k)
	if known == nil {
		return nil
	}
	weakest := known[len(known)-1].DPS
	hi := 0
	for hi < len(teams) && teams[hi].DPS >= weakest {
		hi++
	}
	one := func(Team) float64 { return 1 }
	lo := k
	for lo < hi && !p.Exhausted {
		mid := (lo + hi) / 2
		got, _ := p.pack(teams[:mid], k, one, true)
		switch {
		case got != nil:
			hi, known = mid, got
		case !p.Exhausted:
			lo = mid + 1
		}
	}
	if p.Exhausted {
		return sorted(known)
	}
	// Every team as strong as teams[hi-1] may be used: equal DPS ties.
	for hi < len(teams) && teams[hi].DPS == teams[hi-1].DPS {
		hi++
	}
	if best := p.bestSum(teams[:hi], k); best != nil {
		return best
	}
	return sorted(known)
}

func (p *Problem) bestSum(teams []Team, k int) []Team {
	got, _ := p.pack(teams, k, func(t Team) float64 { return t.DPS }, false)
	return got
}

// pack finds k teams of ts (sorted by DPS) that fit together, maximising the
// total weight; with anyFit it stops at the first k that fit. Branch and
// bound: take the strongest remaining team or leave it out, where the
// remaining teams are always only those that fit with the ones taken.
//
// The bound is a Lagrangian relaxation of the fitting rules. Each resource
// (a character, capacity 1; a limited weapon, capacity the copies owned) gets
// a price; a team's reduced weight is its weight less the prices of what it
// uses. No k fitting teams can weigh more than the remaining capacity of each
// resource times its price, plus the k best reduced weights. Any prices give
// a valid bound; good ones (found once at the root, below) a tight one.
func (p *Problem) pack(ts []Team, k int, weight func(Team) float64, anyFit bool) ([]Team, float64) {
	if len(ts) < k {
		return nil, 0
	}
	// Number the resources and list each team's.
	id := map[string]int{}
	var capacity []int
	res := make([][]int, len(ts))
	for i := range ts {
		for _, r := range p.resources(&ts[i]) {
			j, ok := id[r]
			if !ok {
				j = len(capacity)
				id[r] = j
				capacity = append(capacity, p.capacity(r))
			}
			res[i] = append(res[i], j)
		}
	}
	price := lagrangePrices(ts, res, capacity, k, weight, anyFit)
	reduced := make([]float64, len(ts))
	for i := range ts {
		reduced[i] = weight(ts[i])
		for _, j := range res[i] {
			reduced[i] -= price[j]
		}
	}
	used := make([]int, len(capacity))
	u := newUsage(p.Copies)
	var chosen, best []int
	bestW := math.Inf(-1)
	found := false
	buf := make([]float64, 0, len(ts))
	seen := make([]bool, len(capacity))
	var touched []int
	var dfs func(rem []int, w float64)
	dfs = func(rem []int, w float64) {
		if found || p.step() {
			return
		}
		need := k - len(chosen)
		if need == 0 {
			if w > bestW {
				bestW, best = w, append([]int(nil), chosen...)
				found = anyFit
			}
			return
		}
		if len(rem) < need {
			return
		}
		// Bound.
		bound := w
		buf, touched = buf[:0], touched[:0]
		for _, i := range rem {
			buf = append(buf, reduced[i])
			for _, j := range res[i] {
				if !seen[j] {
					seen[j] = true
					touched = append(touched, j)
					bound += float64(capacity[j]-used[j]) * price[j]
				}
			}
		}
		for _, j := range touched {
			seen[j] = false
		}
		if anyFit {
			// How many more teams could fit at most: every team with a
			// positive reduced weight counts. Prune if that is below need.
			for _, r := range buf {
				bound += max(0, r)
			}
			if bound-w < float64(need)-1e-9 {
				return
			}
		} else {
			sort.Sort(sort.Reverse(sort.Float64Slice(buf)))
			for _, r := range buf[:need] {
				bound += r
			}
			if bound <= bestW+1e-9 {
				return
			}
		}
		first := rem[0]
		u.add(&ts[first], 1)
		for _, j := range res[first] {
			used[j]++
		}
		chosen = append(chosen, first)
		var next []int
		for _, i := range rem[1:] {
			if u.fits(&ts[i]) {
				next = append(next, i)
			}
		}
		dfs(next, w+weight(ts[first]))
		chosen = chosen[:len(chosen)-1]
		for _, j := range res[first] {
			used[j]--
		}
		u.add(&ts[first], -1)
		dfs(rem[1:], w)
	}
	all := make([]int, len(ts))
	for i := range all {
		all[i] = i
	}
	dfs(all, 0)
	if best == nil {
		return nil, 0
	}
	out := make([]Team, len(best))
	for i, j := range best {
		out[i] = ts[j]
	}
	return out, bestW
}

// lagrangePrices chooses resource prices that make the bound tight, by
// subgradient descent on the bound at the root. The bound at prices λ is
// sum(capacity·λ) + the k best reduced weights (with free count: every
// positive reduced weight); its subgradient for a resource is its capacity
// minus how many of the counted teams use it.
func lagrangePrices(ts []Team, res [][]int, capacity []int, k int, weight func(Team) float64, freeCount bool) []float64 {
	price := make([]float64, len(capacity))
	best := make([]float64, len(capacity))
	bestBound := math.Inf(1)
	scale := 0.0
	for _, t := range ts {
		scale = max(scale, weight(t))
	}
	type rw struct {
		i int
		r float64
	}
	rs := make([]rw, len(ts))
	use := make([]int, len(capacity))
	for iter := 0; iter < 500; iter++ {
		bound := 0.0
		for j, v := range price {
			bound += float64(capacity[j]) * v
		}
		for i, t := range ts {
			r := weight(t)
			for _, j := range res[i] {
				r -= price[j]
			}
			rs[i] = rw{i, r}
		}
		sort.Slice(rs, func(a, b int) bool { return rs[a].r > rs[b].r })
		clear(use)
		counted := rs[:k]
		if freeCount {
			n := 0
			for n < len(rs) && rs[n].r > 0 {
				n++
			}
			counted = rs[:n]
		}
		for _, x := range counted {
			bound += x.r
			for _, j := range res[x.i] {
				use[j]++
			}
		}
		if bound < bestBound {
			bestBound = bound
			copy(best, price)
		}
		step := scale / 4 / float64(iter+2)
		for j := range price {
			price[j] = max(0, price[j]-step*float64(capacity[j]-use[j]))
		}
	}
	return best
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
		if p.step() {
			return
		}
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
	p.steps, p.Exhausted = 0, false
	dfs(0, 0, 0, inf)
	out := make([]Team, len(elements))
	for i, j := range best {
		if j >= 0 {
			out[i] = groups[i][j]
		}
	}
	return out
}

// resources lists what a team uses up: each member (capacity 1) and each
// limited weapon copy (capacity: the copies owned). Unlimited weapons are not
// resources.
func (p *Problem) resources(t *Team) []string {
	var rs []string
	for _, c := range t.Members {
		rs = append(rs, "c:"+c)
	}
	for _, w := range t.Weapons {
		if c, ok := p.Copies[w]; ok && c >= 0 {
			rs = append(rs, "w:"+w)
		}
	}
	return rs
}

// capacity of a resource.
func (p *Problem) capacity(r string) int {
	if w, ok := strings.CutPrefix(r, "w:"); ok {
		return p.Copies[w]
	}
	return 1
}
