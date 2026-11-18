package solver

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

func tm(id string, dps float64, el string, members ...string) Team {
	return Team{ID: id, Members: members, Weapons: []string{"w" + id}, Element: el, DPS: dps}
}

func ids(ts []Team) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.ID)
	}
	return out
}

func TestBestAvoidsSharedCharacters(t *testing.T) {
	p := &Problem{Teams: []Team{
		tm("a", 100, "pyro", "x", "y"),
		tm("b", 90, "pyro", "x", "z"),  // shares x with a
		tm("c", 80, "hydro", "y", "z"), // shares y with a, z with b
		tm("d", 60, "cryo", "u", "v"),
	}}
	if got := ids(p.Best(2, Sum)); fmt.Sprint(got) != "[a d]" {
		t.Errorf("sum: got %v", got)
	}
	// b+c: no, they share z. Best pairs: a+d=160, b+d=150, c+d=140.
	if got := ids(p.Best(3, Sum)); len(got) != 2 {
		t.Errorf("only two teams fit together, got %v", got)
	}
}

func TestMinAndSumDiffer(t *testing.T) {
	p := &Problem{Teams: []Team{
		tm("big", 300, "pyro", "a", "b"),
		tm("bigpair", 50, "pyro", "c", "d"),
		tm("even1", 150, "pyro", "a", "c"),
		tm("even2", 140, "pyro", "b", "d"),
	}}
	if got := ids(p.Best(2, Sum)); fmt.Sprint(got) != "[big bigpair]" {
		t.Errorf("sum: %v", got)
	}
	if got := ids(p.Best(2, Min)); fmt.Sprint(got) != "[even1 even2]" {
		t.Errorf("min: %v", got)
	}
}

func TestWeaponCopies(t *testing.T) {
	a := tm("a", 100, "pyro", "x")
	b := tm("b", 90, "hydro", "y")
	c := tm("c", 10, "hydro", "z")
	a.Weapons, b.Weapons, c.Weapons = []string{"jade"}, []string{"jade"}, []string{"dull"}
	p := &Problem{Teams: []Team{a, b, c}, Copies: map[string]int{"jade": 1, "dull": -1}}
	if got := ids(p.Best(2, Sum)); fmt.Sprint(got) != "[a c]" {
		t.Errorf("one jade copy: %v", got)
	}
	p.Copies["jade"] = 2
	if got := ids(p.Best(2, Sum)); fmt.Sprint(got) != "[a b]" {
		t.Errorf("two jade copies: %v", got)
	}
}

func TestByElement(t *testing.T) {
	p := &Problem{Teams: []Team{
		tm("p1", 300, "pyro", "a", "b"),
		tm("p2", 200, "pyro", "c", "d"),
		tm("h1", 250, "hydro", "a", "e"), // clashes with p1
		tm("h2", 100, "hydro", "f", "g"),
	}}
	got := p.ByElement([]string{"pyro", "hydro", "geo"}, Sum)
	// p2 + h1 = 450 beats p1 + h2 = 400; geo has no team and stays empty.
	if fmt.Sprint(ids(got)) != "[p2 h1 ]" {
		t.Errorf("sum: %v", ids(got))
	}
	got = p.ByElement([]string{"pyro", "hydro"}, Min)
	if fmt.Sprint(ids(got)) != "[p2 h1]" {
		t.Errorf("min: %v", ids(got))
	}
}

// bruteBest checks Best against trying every subset, on small random problems.
func TestBestMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	chars := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	for trial := 0; trial < 200; trial++ {
		var teams []Team
		for i := 0; i < 12; i++ {
			m := rng.Perm(len(chars))[:3]
			teams = append(teams, tm(fmt.Sprint(i), float64(rng.Intn(1000)), "x", chars[m[0]], chars[m[1]], chars[m[2]]))
		}
		p := &Problem{Teams: teams}
		for _, o := range []Objective{Sum, Min} {
			k := 3
			got := p.Best(k, o)
			want := -1.0
			for mask := 0; mask < 1<<len(teams); mask++ {
				var pick []Team
				for i := range teams {
					if mask&(1<<i) != 0 {
						pick = append(pick, teams[i])
					}
				}
				if len(pick) != len(got) || !disjoint(pick) {
					continue
				}
				want = max(want, value(o, pick))
			}
			if value(o, got) != want {
				t.Fatalf("trial %d %v: got %v (%v), want %v", trial, o, ids(got), value(o, got), want)
			}
		}
	}
}

func disjoint(ts []Team) bool {
	seen := map[string]bool{}
	for _, t := range ts {
		for _, c := range t.Members {
			if seen[c] {
				return false
			}
			seen[c] = true
		}
	}
	return true
}

func value(o Objective, ts []Team) float64 {
	var d []float64
	for _, t := range ts {
		d = append(d, t.DPS)
	}
	return score(o, d)
}

// A realistic size: 400 teams of 4 from 115 characters, popular characters in
// many teams. It must finish quickly.
func TestBestIsFastAtRealSize(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	var teams []Team
	for i := 0; i < 400; i++ {
		seen := map[int]bool{}
		var m []string
		for len(m) < 4 {
			c := int(rng.ExpFloat64()*25) % 115 // skewed: low numbers are popular
			if !seen[c] {
				seen[c] = true
				m = append(m, fmt.Sprint("c", c))
			}
		}
		el := []string{"pyro", "hydro", "electro", "cryo", "anemo", "geo", "dendro"}[rng.Intn(7)]
		teams = append(teams, tm(fmt.Sprint(i), 100000+rng.Float64()*150000, el, m...))
	}
	p := &Problem{Teams: teams}
	start := time.Now()
	for _, o := range []Objective{Sum, Min} {
		if got := p.Best(10, o); len(got) != 10 {
			t.Errorf("%v: %d teams", o, len(got))
		}
		got := p.ByElement([]string{"pyro", "hydro", "electro", "cryo", "anemo", "geo", "dendro"}, o)
		for _, g := range got {
			if g.ID == "" {
				t.Errorf("%v: an element has no team", o)
			}
		}
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("took %s", d)
	} else {
		t.Logf("took %s", d)
	}
}

// There is one Traveler: two teams with different Traveler elements never go
// together.
func TestOneTraveler(t *testing.T) {
	p := &Problem{Teams: []Team{
		tm("geo", 100, "geo", "luminegeo", "a"),
		tm("dendro", 90, "dendro", "luminedendro", "b"),
		tm("other", 50, "hydro", "c", "d"),
	}}
	if got := ids(p.Best(2, Sum)); fmt.Sprint(got) != "[geo other]" {
		t.Errorf("branch and bound: got %v, want [geo other]", got)
	}
	if h := highsForTest(t); h != nil {
		r, err := p.BestHiGHS(h, 2, Sum)
		if err != nil || fmt.Sprint(ids(r.Teams)) != "[geo other]" {
			t.Errorf("HiGHS: got %v %v, want [geo other]", ids(r.Teams), err)
		}
		p.Require = []string{"luminedendro"} // a pinned Traveler rules out the other element
		r, err = p.BestHiGHS(h, 2, Sum)
		if err != nil || fmt.Sprint(ids(r.Teams)) != "[dendro other]" {
			t.Errorf("HiGHS, Dendro Traveler pinned: got %v %v, want [dendro other]", ids(r.Teams), err)
		}
	}
}
