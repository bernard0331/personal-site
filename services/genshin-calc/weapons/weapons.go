// Package weapons decides which of a player's weapons each team member wields.
//
// The simulator can say how good a weapon is, but simulating every weapon
// combination for every team is far too slow. Instead each character's
// candidate weapons are tried once, in a few of that character's usual teams
// (the weapon trials), which ranks the candidates. Each team then gets the
// best ranking-respecting allocation of the player's copies: no copy is wielded
// by two members, except weapons the player has unlimited copies of.
package weapons

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
	"github.com/bernard0331/personal-site/services/genshin-calc/good"
)

// Choice is one ranked weapon for a character. Score is the team DPS with
// this weapon as a fraction of the best candidate's (so the best is 1),
// averaged over the trial teams.
type Choice struct {
	Weapon string  `json:"weapon"`
	Score  float64 `json:"score"`
}

// Ranking is every tried character's candidates, best first. It is what the
// weapon trials produce and what the passes read.
type Ranking map[string][]Choice

func ReadRanking(path string) (Ranking, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Ranking
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

func (r Ranking) Write(path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Usage counts how often the rotations give each character each weapon. The
// database's authors chose those weapons, so frequent ones are worth trying.
type Usage map[string]map[string]int

func (u Usage) Add(character, weapon string) {
	if u[character] == nil {
		u[character] = map[string]int{}
	}
	u[character][weapon]++
}

// Inventory is the player's weapon copies.
type Inventory struct {
	copies    map[string][]int // refinements of each weapon's copies, best first
	unlimited map[string]bool
}

func NewInventory(acc *good.Account) *Inventory {
	inv := &Inventory{copies: map[string][]int{}, unlimited: acc.Unlimited}
	for _, w := range acc.Weapons {
		inv.copies[w.Key] = append(inv.copies[w.Key], w.Refinement)
	}
	for _, rs := range inv.copies {
		sort.Sort(sort.Reverse(sort.IntSlice(rs)))
	}
	return inv
}

// Owns reports whether the player has a copy of the weapon.
func (inv *Inventory) Owns(weapon string) bool { return len(inv.copies[weapon]) > 0 }

// Best is the refinement of the player's best copy, or 0 if none.
func (inv *Inventory) Best(weapon string) int {
	if rs := inv.copies[weapon]; len(rs) > 0 {
		return rs[0]
	}
	return 0
}

// Copies is how many copies the player has; -1 means unlimited.
func (inv *Inventory) Copies(weapon string) int {
	if inv.unlimited[weapon] {
		return -1
	}
	return len(inv.copies[weapon])
}

// Candidates are the weapons worth trying on a character: every owned 5-star
// of the character's weapon type, the `frequent` owned weapons the rotations
// give the character most often, and the extra weapons (e.g. a rotation's own
// weapon) if owned. If that leaves nothing, the owned weapon of the right type
// with the highest rarity stands in.
func Candidates(ch catalog.Character, cat *catalog.Catalog, inv *Inventory, usage Usage, frequent int, extra ...string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(w string) {
		if !seen[w] && inv.Owns(w) {
			if e, ok := cat.Weapon(w); ok && e.WeaponType == ch.WeaponType {
				seen[w] = true
				out = append(out, w)
			}
		}
	}
	for _, w := range extra {
		add(w)
	}
	var owned []string
	for w := range inv.copies {
		owned = append(owned, w)
	}
	sort.Strings(owned)
	for _, w := range owned {
		if e, _ := cat.Weapon(w); e.Rarity == 5 {
			add(w)
		}
	}
	type use struct {
		w string
		n int
	}
	var uses []use
	for w, n := range usage[ch.Key] {
		uses = append(uses, use{w, n})
	}
	sort.Slice(uses, func(i, j int) bool {
		if uses[i].n != uses[j].n {
			return uses[i].n > uses[j].n
		}
		return uses[i].w < uses[j].w
	})
	taken := 0
	for _, u := range uses {
		if taken == frequent {
			break
		}
		if !seen[u.w] && inv.Owns(u.w) {
			add(u.w)
			if seen[u.w] {
				taken++
			}
		}
	}
	if len(out) == 0 {
		best := ""
		for _, w := range owned {
			e, _ := cat.Weapon(w)
			if e.WeaponType == ch.WeaponType {
				if b, _ := cat.Weapon(best); best == "" || e.Rarity > b.Rarity {
					best = w
				}
			}
		}
		if best != "" {
			out = append(out, best)
		}
	}
	return out
}

// Fallback is every owned weapon of the character's type, the preferred one
// first (if owned), then by rarity, best first. It is the stand-in order for
// characters the trials did not rank.
func Fallback(ch catalog.Character, cat *catalog.Catalog, inv *Inventory, preferred string) []string {
	var out []string
	if inv.Owns(preferred) {
		if e, _ := cat.Weapon(preferred); e.WeaponType == ch.WeaponType {
			out = append(out, preferred)
		}
	}
	var rest []catalog.Weapon
	for w := range inv.copies {
		if e, ok := cat.Weapon(w); ok && e.WeaponType == ch.WeaponType && w != preferred && len(inv.copies[w]) > 0 {
			rest = append(rest, e)
		}
	}
	sort.Slice(rest, func(i, j int) bool {
		if rest[i].Rarity != rest[j].Rarity {
			return rest[i].Rarity > rest[j].Rarity
		}
		return rest[i].Key < rest[j].Key
	})
	for _, e := range rest {
		out = append(out, e.Key)
	}
	return out
}

// Pick is the weapon copy one member wields.
type Pick struct {
	Weapon     string
	Refinement int
}

// Allocate gives each member a weapon copy. wants[i] is member i's ranked
// choices (best first); a member left without a ranked choice gets the first
// available weapon of fallback[i] (ordered, best first). Members
// compete for limited copies by regret: whoever would lose the most by taking
// their next choice instead is served first. It fails only if a member has
// nothing left to wield.
func Allocate(wants [][]Choice, fallback [][]string, inv *Inventory) ([]Pick, error) {
	n := len(wants)
	picks := make([]Pick, n)
	done := make([]bool, n)
	used := map[string]int{}
	available := func(w string) bool {
		c := inv.Copies(w)
		return c < 0 || used[w] < c
	}
	// next is member i's best still-available choice and the one after it.
	next := func(i int) (first, second int) {
		first, second = -1, -1
		for k, c := range wants[i] {
			if !available(c.Weapon) {
				continue
			}
			if first < 0 {
				first = k
			} else {
				second = k
				break
			}
		}
		return
	}
	for left := n; left > 0; left-- {
		who, whoFirst, worst := -1, -1, -1.0
		for i := 0; i < n; i++ {
			if done[i] {
				continue
			}
			first, second := next(i)
			if first < 0 {
				continue
			}
			regret := 1.0 // nothing else to wield: serve first
			if second >= 0 {
				regret = wants[i][first].Score - wants[i][second].Score
			}
			if regret > worst {
				who, whoFirst, worst = i, first, regret
			}
		}
		if who < 0 {
			break // the rest have no ranked choice left
		}
		w := wants[who][whoFirst].Weapon
		picks[who] = take(inv, used, w)
		done[who] = true
	}
	for i := 0; i < n; i++ {
		if done[i] {
			continue
		}
		got := false
		for _, w := range fallback[i] {
			if available(w) {
				picks[i], got = take(inv, used, w), true
				break
			}
		}
		if !got {
			return nil, fmt.Errorf("member %d has no weapon left to wield", i+1)
		}
	}
	return picks, nil
}

// take uses the best unused copy of w.
func take(inv *Inventory, used map[string]int, w string) Pick {
	r := 5
	if inv.Copies(w) >= 0 {
		r = inv.copies[w][used[w]]
	} else if b := inv.Best(w); b > 0 {
		r = b
	}
	used[w]++
	return Pick{Weapon: w, Refinement: r}
}

// Score turns trial DPS into a ranking. dps[trial][weapon] is the team DPS of
// one trial team with each weapon; a weapon's score is its DPS as a fraction
// of the trial's best, averaged over the trials it was tried in.
func Score(dps []map[string]float64) []Choice {
	sum, n := map[string]float64{}, map[string]int{}
	for _, trial := range dps {
		best := 0.0
		for _, v := range trial {
			best = max(best, v)
		}
		if best <= 0 {
			continue
		}
		for w, v := range trial {
			sum[w] += v / best
			n[w]++
		}
	}
	var out []Choice
	for w := range sum {
		out = append(out, Choice{Weapon: w, Score: sum[w] / float64(n[w])})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Weapon < out[j].Weapon
	})
	return out
}
