package gear

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bernard0331/personal-site/services/genshin-calc/coarse"
	"github.com/bernard0331/personal-site/services/genshin-calc/good"
)

const team = `fischl char lvl=90/90 cons=6 talent=10,10,10;
fischl add weapon="polarstar" refine=1 lvl=90/90;
fischl add set="goldentroupe" count=4;
fischl add stats hp=4780 atk=311 atk%=0.466 electro%=0.466 cr=0.311; #main
fischl add stats er=0.0551*2 cr=0.0331*10 cd=0.0662*8;
bennett char lvl=90/90 cons=6 talent=10,10,10;
bennett add set="noblesseoblige" count=4;
bennett add stats er=0.518;
options swap_delay=12 iteration=1000;
active fischl;
`

func TestConfigEditing(t *testing.T) {
	st := ArtifactStats(team, "fischl")
	if math.Abs(st["critRate_"]-(31.1+33.1)) > 1e-6 || math.Abs(st["enerRech_"]-11.02) > 1e-6 || st["atk"] != 311 {
		t.Fatalf("stats %v", st)
	}
	if got := Sets(team, "bennett"); got["noblesseoblige"] != 4 {
		t.Fatalf("sets %v", got)
	}
	cfg, err := WithGear(team, "fischl", map[string]int{"tenacityofthemillelith": 4, "gladiatorsfinale": 1}, Stats{"critRate_": 50, "atk": 300})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cfg, "goldentroupe") || strings.Contains(cfg, "gladiatorsfinale") || !strings.Contains(cfg, `fischl add set="tenacityofthemillelith" count=4;`) {
		t.Fatalf("sets not replaced:\n%s", cfg)
	}
	if !strings.Contains(cfg, "fischl add stats atk=300 cr=0.5;") || !strings.Contains(cfg, `bennett add set="noblesseoblige"`) {
		t.Fatalf("stats not replaced, or bennett touched:\n%s", cfg)
	}
	opts := Options(team, 300, true)
	if !strings.Contains(opts, "options swap_delay=12 iteration=300 ignore_burst_energy=true;") {
		t.Fatalf("options: %s", opts)
	}
	if got := Characters(team); len(got) != 2 || got[0] != "fischl" || got[1] != "bennett" {
		t.Fatalf("characters %v", got)
	}
}

func TestPieces(t *testing.T) {
	ps := Pieces([]good.Artifact{
		{Set: "goldentroupe", Slot: "sands", Rarity: 5, Level: 20, MainStat: "atk_", Substats: []good.Substat{{Key: "critRate_", Value: 3.9}}},
		{Set: "goldentroupe", Slot: "flower", Rarity: 3, Level: 12, MainStat: "hp"},
	})
	if len(ps) != 1 || math.Abs(ps[0].Stats["atk_"]-46.6) > 1e-9 || ps[0].Stats["critRate_"] != 3.9 {
		t.Fatalf("%+v", ps)
	}
}

func TestERNeed(t *testing.T) {
	p, err := NewPlan(team, []Character{{Key: "fischl", Element: "electro"}}, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	own := p.er0["fischl"] // 11.02 from the substats
	res := map[string]coarse.Result{"base": {DPS: 1000}}
	for _, j := range p.Jobs {
		if o, ok := strings.CutPrefix(j.ID, "er/fischl/"); ok {
			var off float64
			if _, err := fmt.Sscan(o, &off); err != nil {
				t.Fatal(err)
			}
			wait := 0.0
			if own+off < 40 { // waits until 40% ER
				wait = 3
			}
			res[j.ID] = coarse.Result{EnergyWaits: []float64{wait, 0}}
		}
	}
	m, err := p.Analyze(res)
	if err != nil {
		t.Fatal(err)
	}
	if m[0].ERNeed < 40 || m[0].ERNeed > 40+erStep {
		t.Fatalf("ER need %.2f, want 40 to 45", m[0].ERNeed)
	}
}

func TestAllocate(t *testing.T) {
	py, _ := filepath.Abs("../../../local/highs-venv/bin/python")
	if _, err := os.Stat(py); err != nil {
		t.Skip("no local HiGHS Python")
	}
	mk := func(id int, set, slot string, st Stats) Piece {
		return Piece{ID: id, Set: set, Slot: slot, Rarity: 5, Level: 20, Stats: st}
	}
	var ps []Piece
	id := 0
	for _, slot := range Slots {
		// One great crit piece both want, and filler.
		ps = append(ps, mk(id, "goldentroupe", slot, Stats{"critRate_": 20}))
		ps = append(ps, mk(id+1, "goldentroupe", slot, Stats{"critRate_": 10}))
		ps = append(ps, mk(id+2, "emblemofseveredfate", slot, Stats{"enerRech_": 12}))
		id += 3
	}
	a := &Allocator{Python: py}
	ws := []Wearer{
		{Measured: Measured{Char: "a", Weights: Stats{"critRate_": 100}, ERNeed: 0}},
		{Measured: Measured{Char: "b", Weights: Stats{"critRate_": 50}, ERNeed: 30}}, // needs 3 ER pieces
	}
	al, err := a.Allocate(ws, ps, "sum", []float64{0})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	for _, as := range al.Assignments {
		if len(as.Pieces) != 5 {
			t.Fatalf("%s got %d pieces", as.Char, len(as.Pieces))
		}
		for _, p := range as.Pieces {
			if seen[p.ID] {
				t.Fatalf("piece %d given twice", p.ID)
			}
			seen[p.ID] = true
		}
	}
	if b := al.Assignments[1]; b.ER < 30 {
		t.Fatalf("b has %.0f ER, needs 30", b.ER)
	}
	if a0 := al.Assignments[0]; a0.Stats["critRate_"] < 100 {
		t.Fatalf("a should get every best crit piece, got %v", a0.Stats)
	}
}
