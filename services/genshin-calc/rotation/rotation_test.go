package rotation

import (
	"os"
	"strings"
	"testing"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
)

func load(t *testing.T) (*Rotation, *catalog.Catalog) {
	t.Helper()
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile("../rotations/skirk-vodyanitsa-escoffier-furina.txt")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Parse(string(text), cat)
	if err != nil {
		t.Fatal(err)
	}
	return r, cat
}

var loadout = map[string]Slot{
	"skirk":      {Constellation: 0, Level: 90, Weapon: "azurelight", Refinement: 1},
	"vodyanitsa": {Constellation: 0, Level: 90, Weapon: "thrillingtalesofdragonslayers", Refinement: 5},
	"escoffier":  {Constellation: 2, Level: 100, Weapon: "favoniuslance", Refinement: 5},
	"furina":     {Constellation: 0, Level: 90, Weapon: "favoniussword", Refinement: 5},
}

func TestParseFindsTheTeamAndItsWeapons(t *testing.T) {
	r, _ := load(t)
	if got := strings.Join(r.Team(), " "); got != "skirk vodyanitsa escoffier furina" {
		t.Errorf("team = %q", got)
	}
	if r.Members[2].Weapon != "symphonistofscents" {
		t.Errorf("escoffier's example weapon = %q", r.Members[2].Weapon)
	}
}

func TestConfigUsesThePlayersCharactersAndWeapons(t *testing.T) {
	r, _ := load(t)
	cfg, err := r.Config(loadout)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"skirk char lvl=90/90 cons=0 talent=10,10,10;",
		"escoffier char lvl=100/100 cons=2 talent=10,10,10;",
		`escoffier add weapon="favoniuslance" refine=5 lvl=90/90;`,
		`escoffier add set="noblesseoblige" count=4;`,
		"active furina;",
		"vodyanitsa skill;",
		"target lvl=100",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config is missing %q", want)
		}
	}
	for _, gone := range []string{"symphonistofscents", "talent=9,9,9"} {
		if strings.Contains(cfg, gone) {
			t.Errorf("config still contains the example build's %q", gone)
		}
	}
	if strings.Count(cfg, " char lvl=") != 4 || strings.Count(cfg, "add weapon=") != 4 {
		t.Errorf("config should define four characters and four weapons:\n%s", cfg)
	}
}

func TestShortNamesAndSettingsAreKept(t *testing.T) {
	cat, _ := catalog.Load()
	text := `
bina char lvl=90/90 cons=0 talent=9,9,9;
bina add weapon="pamber" refine=5 lvl=90/90;
hutao char lvl=90/90 cons=1 talent=10,10,10 +params=[start_hp=1];
hutao add weapon="dbane" refine=5 lvl=90/90;
varka char lvl=90/90 cons=0 talent=9,9,9;
varka add weapon="serpentspine" refine=1 lvl=90/90 +params=[stacks=5];
aethercryo char lvl=90/90 cons=6 talent=9,9,9;
aethercryo add weapon="favoniussword" refine=5 lvl=90/90;
active bina;
bina skill;`
	r, err := Parse(text, cat)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.Team(), " "); got != "columbina hutao varka luminecryo" {
		t.Errorf("team = %q", got)
	}
	slots := map[string]Slot{
		"columbina":  {Level: 90, Weapon: "prototypeamber", Refinement: 5},
		"hutao":      {Level: 90, Constellation: 1, Weapon: "dragonsbane", Refinement: 5},
		"varka":      {Level: 90, Weapon: "serpentspine", Refinement: 5},
		"luminecryo": {Level: 90, Constellation: 6, Weapon: "skywardblade", Refinement: 1},
	}
	cfg, err := r.Config(slots)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"bina char lvl=90/90",                    // the script's own name for the character
		"+params=[start_hp=1];",                  // a character setting is kept
		`refine=5 lvl=90/90 +params=[stacks=5];`, // a weapon setting is kept for the same weapon
		`aethercryo add weapon="skywardblade"`,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config is missing %q:\n%s", want, cfg)
		}
	}
	slots["varka"] = Slot{Level: 90, Weapon: "wolfsgravestone", Refinement: 1}
	if cfg, _ = r.Config(slots); strings.Contains(cfg, "stacks=5") {
		t.Error("a weapon setting was carried over to a different weapon")
	}
}

func TestConfigRefusesBadLoadouts(t *testing.T) {
	r, _ := load(t)
	bad := func(change func(map[string]Slot)) map[string]Slot {
		m := map[string]Slot{}
		for k, v := range loadout {
			m[k] = v
		}
		change(m)
		return m
	}
	cases := map[string]map[string]Slot{
		"missing member": bad(func(m map[string]Slot) { delete(m, "furina") }),
		"level 80":       bad(func(m map[string]Slot) { s := m["skirk"]; s.Level = 80; m["skirk"] = s }),
		"refinement 6":   bad(func(m map[string]Slot) { s := m["skirk"]; s.Refinement = 6; m["skirk"] = s }),
		"script in key":  bad(func(m map[string]Slot) { s := m["skirk"]; s.Weapon = `x"; skirk burst; "`; m["skirk"] = s }),
	}
	for name, l := range cases {
		if _, err := r.Config(l); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestParseRefusesWhatTheSimulatorCannotRun(t *testing.T) {
	cat, _ := catalog.Load()
	for name, text := range map[string]string{
		"unknown character": "nobody char lvl=90/90 cons=0 talent=9,9,9;",
		"three characters": `skirk char lvl=90/90 cons=0 talent=9,9,9;
skirk add weapon="azurelight" refine=1 lvl=90/90;
furina char lvl=90/90 cons=0 talent=9,9,9;
furina add weapon="favoniussword" refine=5 lvl=90/90;
mona char lvl=90/90 cons=0 talent=9,9,9;
mona add weapon="ttds" refine=5 lvl=90/90;`,
		"unknown weapon": `skirk char lvl=90/90 cons=0 talent=9,9,9;
skirk add weapon="notaweapon" refine=1 lvl=90/90;`,
	} {
		if _, err := Parse(text, cat); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestWeaponLockedWhenTheScriptWaitsOnIt(t *testing.T) {
	cat, _ := catalog.Load()
	text := `bennett char lvl=90/90 cons=6 talent=9,9,9;
bennett add weapon="favoniussword" refine=3 lvl=90/90;
xiangling char lvl=90/90 cons=6 talent=9,9,9;
xiangling add weapon="thecatch" refine=5 lvl=90/90;
xingqiu char lvl=90/90 cons=6 talent=9,9,9;
xingqiu add weapon="sacrificialsword" refine=5 lvl=90/90;
sucrose char lvl=90/90 cons=6 talent=9,9,9;
sucrose add weapon="favoniuscodex" refine=5 lvl=90/90;
while !.bennett.mods.favonius-cd { bennett attack; }
# while !.sucrose.mods.favonius-cd { sucrose attack; }
`
	r, err := Parse(text, cat)
	if err != nil {
		t.Fatal(err)
	}
	locked := map[string]bool{}
	for _, m := range r.Members {
		locked[m.Key] = m.WeaponLocked
	}
	if !locked["bennett"] || locked["xiangling"] || locked["xingqiu"] || locked["sucrose"] {
		t.Errorf("only bennett's script check counts (comments do not): %v", locked)
	}
}

func TestScaleEnergy(t *testing.T) {
	in := "x;\nenergy every interval=480,720 amount=1; # standard\nenergy every interval=600 amount=2;\n"
	want := "x;\nenergy every interval=240,360 amount=1; # standard\nenergy every interval=300 amount=2;\n"
	if got := ScaleEnergy(in, 2); got != want {
		t.Errorf("got %q", got)
	}
	if got := ScaleEnergy(in, 1); got != in {
		t.Errorf("rate 1 must not change the config")
	}
}
