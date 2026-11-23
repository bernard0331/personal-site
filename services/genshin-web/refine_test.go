package main

import (
	"testing"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
	"github.com/bernard0331/personal-site/services/genshin-calc/coarse"
)

func TestSubstats(t *testing.T) {
	cat, _ := catalog.Load()
	r := coarse.Result{Stats: []string{
		"mualani add stats hp=4780 atk=311 hp%=0.466 hp%=0.466 cd=0.622;",
		"mualani add stats def%=0.062*2 hp%=0.0496*8 cr=0.0331*6 cd=0.0662*10 em=19.82*4;",
		"arle add stats def%=0.062*2 cr=0.0331*12 cd=0.0662*12;",
	}}
	got := substats(r, cat)
	if want := "CRIT DMG 8 · HP% 6 · CRIT Rate 4 · EM 2"; got["mualani"] != want {
		t.Errorf("got %q, want %q", got["mualani"], want)
	}
	if got["arlecchino"] == "" {
		t.Errorf("an alias (arle) must be stored under the character's key: %v", got)
	}
}

func TestCandidates(t *testing.T) {
	res := func(id string, dps float64, team ...string) coarse.Result {
		ws := make([]string, len(team))
		for i, c := range team {
			ws[i] = c + ": dullblade R1"
		}
		return coarse.Result{Source: "own", ID: id, Team: team, Weapons: ws, DPS: dps,
			ElementDPS: map[string]float64{"pyro": dps}}
	}
	results := []coarse.Result{
		res("a", 300, "a1", "a2", "a3", "a4"),
		res("b", 200, "b1", "b2", "b3", "b4"),
		res("c", 100, "c1", "c2", "c3", "c4"),
		res("d", 50, "d1", "d2", "d3", "d4"),
	}
	// The quick run chose team c, with another weapon on c1.
	quick := &Result{Breakdowns: []Breakdown{{Teams: []TeamView{{Source: "own", Rotation: "c", Members: []MemberView{
		{Key: "c1", Weapon: "silversword", Refinement: 5}, {Key: "c2", Weapon: "dullblade", Refinement: 1},
		{Key: "c3", Weapon: "dullblade", Refinement: 1}, {Key: "c4", Weapon: "dullblade", Refinement: 1}}}}}}}
	e := &engine{}
	got := e.candidates(results, quick, RefineOptions{Top: 1}, []string{"d2"})
	var ids []string
	for _, r := range got {
		ids = append(ids, r.ID)
	}
	if len(got) != 3 || ids[0] != "c" || ids[1] != "a" || ids[2] != "d" {
		t.Fatalf("got %v, want the quick choice c, the best team a, and pinned d2's team d", ids)
	}
	if got[0].Weapons[0] != "c1: silversword R5" {
		t.Errorf("the quick choice keeps its chosen weapons, got %v", got[0].Weapons)
	}
}
