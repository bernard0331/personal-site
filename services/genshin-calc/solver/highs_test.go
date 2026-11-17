package solver

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// highsForTest finds the project's HiGHS Python, or skips.
func highsForTest(t *testing.T) *HiGHS {
	py := os.Getenv("GENSHIN_HIGHS_PYTHON")
	if py == "" {
		py, _ = filepath.Abs("../../../local/highs-venv/bin/python")
	}
	if _, err := os.Stat(py); err != nil {
		t.Skip("no HiGHS Python (set GENSHIN_HIGHS_PYTHON)")
	}
	return &HiGHS{Python: py}
}

// HiGHS must agree with the branch and bound, which the brute-force test
// checks, on random problems with weapon copies and elements.
func TestHiGHSMatchesBranchAndBound(t *testing.T) {
	h := highsForTest(t)
	rng := rand.New(rand.NewSource(3))
	chars := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"}
	els := []string{"pyro", "hydro", "cryo"}
	for trial := 0; trial < 25; trial++ {
		var teams []Team
		for i := 0; i < 30; i++ {
			m := rng.Perm(len(chars))[:4]
			tm := Team{ID: fmt.Sprint(i), Element: els[rng.Intn(3)], DPS: float64(100 + rng.Intn(900)),
				Members: []string{chars[m[0]], chars[m[1]], chars[m[2]], chars[m[3]]},
				Weapons: []string{[]string{"jade", "fav", "tt", "dull"}[rng.Intn(4)], "x", "y", "z"}}
			teams = append(teams, tm)
		}
		p := &Problem{Teams: teams, Copies: map[string]int{"jade": 1, "fav": 2, "tt": -1}}
		for _, o := range []Objective{Sum, Min} {
			want := value(o, p.Best(3, o))
			got, err := p.BestHiGHS(h, 3, o)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Optimal || value(o, got.Teams) != want {
				t.Fatalf("trial %d %v best: HiGHS %v (optimal %v), branch and bound %v", trial, o, value(o, got.Teams), got.Optimal, want)
			}
			wantE := nonzero(p.ByElement(els, o))
			gotE, err := p.ByElementHiGHS(h, els, o)
			if err != nil {
				t.Fatal(err)
			}
			if value(o, nonzero(gotE.Teams)) != value(o, wantE) || len(nonzero(gotE.Teams)) != len(wantE) {
				t.Fatalf("trial %d %v by element: HiGHS %v, branch and bound %v", trial, o, ids(gotE.Teams), ids(wantE))
			}
		}
	}
}

func nonzero(ts []Team) []Team {
	var out []Team
	for _, t := range ts {
		if t.ID != "" {
			out = append(out, t)
		}
	}
	return out
}
