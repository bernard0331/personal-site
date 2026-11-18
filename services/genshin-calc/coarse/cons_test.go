package coarse

import "testing"

func TestBetterPrefersMatchingConstellations(t *testing.T) {
	res := func(dps float64, rot []int) Result {
		return Result{Team: []string{"varka", "prune", "nicole", "durin"}, DPS: dps,
			CharDPS: []float64{dps * .8, 1, 1, 1}, RotCons: rot, PlayerCons: []int{0, 4, 0, 0}}
	}
	exact := res(120, []int{0, 4, 0, 0})
	lower := res(150, []int{0, 2, 0, 0})
	higher := res(200, []int{6, 6, 6, 6}) // needs constellations the player lacks
	if exact.ConsTier() != 0 || lower.ConsTier() != 1 || higher.ConsTier() != 2 {
		t.Fatalf("tiers %d %d %d", exact.ConsTier(), lower.ConsTier(), higher.ConsTier())
	}
	if !Better(exact, lower) || !Better(lower, higher) {
		t.Error("an exact match must beat a lower one, which must beat one needing missing constellations")
	}
	if got := higher.MissingCons(); got != "varka C6, prune C6, nicole C6, durin C6" {
		t.Errorf("missing: %q", got)
	}
	if !Better(exact, res(90, []int{0, 4, 0, 0})) {
		t.Error("same match: higher DPS wins")
	}
	best := BestPerTeam([]Result{higher, lower, exact})
	if len(best) != 1 || best[TeamKey(exact.Team)].DPS != 120 {
		t.Errorf("BestPerTeam should keep the exact match: %+v", best)
	}
}
