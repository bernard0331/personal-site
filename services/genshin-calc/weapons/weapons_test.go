package weapons

import (
	"reflect"
	"testing"

	"github.com/bernard0331/personal-site/services/genshin-calc/good"
)

func inv(unlimited []string, copies ...good.Weapon) *Inventory {
	acc := &good.Account{Weapons: copies, Unlimited: map[string]bool{}}
	for _, u := range unlimited {
		acc.Unlimited[u] = true
	}
	return NewInventory(acc)
}

func TestAllocateByRegret(t *testing.T) {
	// One jade. Member 0 loses little without it (0.98 next), member 1 a lot
	// (0.80 next), so member 1 gets it even though member 0 comes first.
	in := inv(nil, good.Weapon{Key: "jade", Refinement: 1}, good.Weapon{Key: "fav", Refinement: 5}, good.Weapon{Key: "black", Refinement: 3})
	wants := [][]Choice{
		{{"jade", 1}, {"fav", 0.98}},
		{{"jade", 1}, {"black", 0.80}},
	}
	got, err := Allocate(wants, [][]string{nil, nil}, in)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Pick{{"fav", 5}, {"jade", 1}}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAllocateCopiesAndUnlimited(t *testing.T) {
	in := inv([]string{"tt"}, good.Weapon{Key: "fav", Refinement: 5}, good.Weapon{Key: "fav", Refinement: 2}, good.Weapon{Key: "tt", Refinement: 5})
	wants := [][]Choice{{{"fav", 1}}, {{"fav", 1}}, {{"tt", 1}}, {{"tt", 1}}}
	got, err := Allocate(wants, make([][]string, 4), in)
	if err != nil {
		t.Fatal(err)
	}
	// Two favonius copies, best refinement to whoever is served first; the
	// unlimited weapon goes to both.
	refs := map[int]bool{got[0].Refinement: true, got[1].Refinement: true}
	if !refs[5] || !refs[2] || got[2] != (Pick{"tt", 5}) || got[3] != (Pick{"tt", 5}) {
		t.Errorf("got %v", got)
	}
}

func TestAllocateFallsBack(t *testing.T) {
	in := inv(nil, good.Weapon{Key: "jade", Refinement: 1}, good.Weapon{Key: "dull", Refinement: 5})
	wants := [][]Choice{{{"jade", 1}}, nil}
	got, err := Allocate(wants, [][]string{{"jade"}, {"jade", "dull"}}, in)
	if err != nil {
		t.Fatal(err)
	}
	if got[1] != (Pick{"dull", 5}) {
		t.Errorf("member 2 should fall back past the taken jade: %v", got)
	}
	if _, err := Allocate([][]Choice{{{"jade", 1}}, nil}, [][]string{nil, {"jade"}}, in); err == nil {
		t.Error("want an error when nothing is left to wield")
	}
}

func TestScore(t *testing.T) {
	got := Score([]map[string]float64{
		{"a": 100, "b": 50},
		{"a": 80, "b": 80},
	})
	want := []Choice{{"a", 1}, {"b", 0.75}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAlternatives(t *testing.T) {
	in := inv([]string{"tt"},
		good.Weapon{Key: "azure", Refinement: 1}, good.Weapon{Key: "mist", Refinement: 1},
		good.Weapon{Key: "alley", Refinement: 2}, good.Weapon{Key: "fav", Refinement: 5},
		good.Weapon{Key: "tt", Refinement: 5})
	ranking := Ranking{
		"bennett": {{"azure", 1}, {"mist", 0.99}, {"alley", 0.975}, {"fav", 0.80}},
		"sucrose": {{"tt", 1}},
	}
	got := Alternatives([]string{"bennett", "sucrose"}, []string{"azure", "tt"}, []bool{false, false}, ranking, in, 0.05, 10)
	var desc []string
	for _, v := range got {
		desc = append(desc, v.Weapons[0])
	}
	// fav is 20% worse: out. mist and alley are near: in, strongest first.
	if want := []string{"azure", "mist", "alley"}; !reflect.DeepEqual(desc, want) {
		t.Errorf("got %v, want %v", desc, want)
	}
	if got[1].Factor != 0.99 {
		t.Errorf("factor %v", got[1].Factor)
	}
	fixed := Alternatives([]string{"bennett", "sucrose"}, []string{"azure", "tt"}, []bool{true, false}, ranking, in, 0.05, 10)
	if len(fixed) != 1 {
		t.Errorf("a fixed member keeps the weapon: %v", fixed)
	}
}
