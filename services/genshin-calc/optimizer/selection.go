package optimizer

import (
	"fmt"
	"strings"

	"github.com/bernard0331/personal-site/services/genshin-calc/coarse"
	"github.com/bernard0331/personal-site/services/genshin-calc/solver"
	"github.com/bernard0331/personal-site/services/genshin-calc/weapons"
)

// Elements are the seven a team can be counted under. A team whose largest damage
// share is physical belongs to none of them.
var Elements = []string{"pyro", "hydro", "electro", "cryo", "anemo", "geo", "dendro"}

// Alternatives says how far the solver may re-arm teams.
type Alternatives struct {
	Rule   *Rule
	Within float64
	Max    int
	// Confirmed holds weapon variants that have since been simulated (keyed
	// by ConfirmKey): the solver gets their real result instead of the
	// estimate. The confirmation pass fills it.
	Confirmed map[string]coarse.Result
}

// ConfirmKey names one rotation with one set of weapons (catalog keys, in
// the rotation's member order).
func ConfirmKey(rotation string, weapons []string) string {
	return rotation + "|" + strings.Join(weapons, ",")
}

// Estimated reports whether a team id from Problem is a weapon variant whose
// DPS is estimated rather than simulated.
func Estimated(id string) bool { return strings.Contains(id, "~") }

// Problem turns results into solver teams: each team's best successful
// rotation, in its simulated weapons and in the weapon variants the trials
// rank close behind (DPS estimated from the trials). The variants share the
// team's four characters, so the solver takes at most one of them. byID finds
// a team's result again, with a variant's weapons and estimate filled in.
func Problem(results []coarse.Result, alt Alternatives) (*solver.Problem, map[string]coarse.Result) {
	best := coarse.BestPerTeam(results)
	inv := alt.Rule.Inv
	p := &solver.Problem{Copies: map[string]int{}}
	byID := map[string]coarse.Result{}
	for _, r := range best {
		id := r.Source + "/" + r.ID
		current := make([]string, len(r.Weapons))
		fixed := make([]bool, len(r.Weapons))
		for i, w := range r.Weapons {
			current[i] = WeaponKey(w)
			// Favonius and Sacrificial weapons may be ones the rotation's
			// script waits on (see rotation.Member.WeaponLocked): keep them.
			fixed[i] = strings.HasPrefix(current[i], "favonius") || strings.HasPrefix(current[i], "sacrificial")
		}
		vs := []weapons.Variant{{Weapons: current, Factor: 1}}
		if alt.Rule.Ranking != nil && alt.Max > 1 {
			vs = weapons.Alternatives(r.Team, current, fixed, alt.Rule.Ranking, inv, alt.Within, alt.Max)
		}
		for v, variant := range vs {
			vid, vr := id, r
			if c, ok := alt.Confirmed[ConfirmKey(id, variant.Weapons)]; ok && v > 0 {
				// Simulated since: a real result, not an estimate.
				vid, vr = fmt.Sprintf("%s=%d", id, v), c
			} else if v > 0 {
				vid = fmt.Sprintf("%s~%d", id, v)
				vr.DPS, vr.DPSSD = r.DPS*variant.Factor, r.DPSSD*variant.Factor
				// The split is the simulated one scaled by the estimated change
				// (a few percent at most): close enough to show, and flagged.
				vr.CharDPS = make([]float64, len(r.CharDPS))
				for i, d := range r.CharDPS {
					vr.CharDPS[i] = d * variant.Factor
				}
				vr.Weapons = nil
				for i, w := range variant.Weapons {
					vr.Weapons = append(vr.Weapons, fmt.Sprintf("%s: %s R%d", r.Team[i], w, inv.Best(w)))
				}
				vr.Flags = append([]string{fmt.Sprintf("weapons changed after simulating: DPS estimated (%+.1f%%)", 100*(variant.Factor-1))}, r.Flags...)
			}
			byID[vid] = vr
			for _, w := range variant.Weapons {
				p.Copies[w] = inv.Copies(w)
			}
			p.Teams = append(p.Teams, solver.Team{
				ID: vid, Members: r.Team, Weapons: variant.Weapons, Element: coarse.MainElement(vr), DPS: vr.DPS,
			})
		}
	}
	return p, byID
}

// WeaponKey takes the weapon out of "member: weapon Rn".
func WeaponKey(s string) string {
	_, rest, _ := strings.Cut(s, ": ")
	key, _, _ := strings.Cut(rest, " ")
	return key
}
