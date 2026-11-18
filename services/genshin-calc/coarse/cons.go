package coarse

import (
	"fmt"
	"strings"
)

// Rotations are written for particular constellations, and many change at
// C2 or C6. A team's result should come from a rotation written for the
// player's own constellations where one exists. ConsTier ranks a result's
// rotation:
//
//	0: written for exactly the player's constellations
//	1: written for constellations at or below the player's (valid, but may
//	   not use everything the player's constellations offer)
//	2: needs a constellation the player lacks (often wrong for them: a C6
//	   rotation's timing can break at C0)
//
// Results without constellation information count as tier 1.
func (r Result) ConsTier() int {
	if len(r.RotCons) != len(r.Team) || len(r.PlayerCons) != len(r.Team) {
		return 1
	}
	tier := 0
	for i := range r.Team {
		switch {
		case r.RotCons[i] > r.PlayerCons[i]:
			return 2
		case r.RotCons[i] < r.PlayerCons[i]:
			tier = 1
		}
	}
	return tier
}

// consGaps are the total constellation difference and the carry's (the
// member dealing the most damage in this result).
func (r Result) consGaps() (total, carry int) {
	if len(r.RotCons) != len(r.Team) || len(r.PlayerCons) != len(r.Team) {
		return 0, 0
	}
	top := 0
	for i := range r.CharDPS {
		if r.CharDPS[i] > r.CharDPS[top] {
			top = i
		}
	}
	for i := range r.Team {
		d := r.RotCons[i] - r.PlayerCons[i]
		if d < 0 {
			d = -d
		}
		total += d
		if i == top {
			carry = d
		}
	}
	return total, carry
}

// MissingCons names the constellations the rotation needs that the player
// lacks, e.g. "prune C6", or "" if none.
func (r Result) MissingCons() string {
	if len(r.RotCons) != len(r.Team) || len(r.PlayerCons) != len(r.Team) {
		return ""
	}
	var need []string
	for i, c := range r.Team {
		if r.RotCons[i] > r.PlayerCons[i] {
			need = append(need, fmt.Sprintf("%s C%d", c, r.RotCons[i]))
		}
	}
	return strings.Join(need, ", ")
}

// Better reports whether a represents its team better than b: a rotation
// matching the player's constellations beats a higher DPS from one that
// does not. Order: lower ConsTier; then the carry's constellation closer to
// the player's; then all members closer; then higher DPS. Comparing the
// members equally first and the carry second was checked on the owner's
// quick pass: putting the carry first kept rotations whose supports needed
// constellations the player lacks, which broke them.
func Better(a, b Result) bool {
	if ta, tb := a.ConsTier(), b.ConsTier(); ta != tb {
		return ta < tb
	}
	ga, ca := a.consGaps()
	gb, cb := b.consGaps()
	if ca != cb {
		return ca < cb
	}
	if ga != gb {
		return ga < gb
	}
	return a.DPS > b.DPS
}

// BestPerTeam keeps each team's best successful result by Better.
func BestPerTeam(results []Result) map[string]Result {
	best := map[string]Result{}
	for _, r := range results {
		if r.Error != "" {
			continue
		}
		k := TeamKey(r.Team)
		if b, ok := best[k]; !ok || Better(r, b) {
			best[k] = r
		}
	}
	return best
}
