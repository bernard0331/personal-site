package good

import (
	"strings"
	"testing"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
)

func parse(t *testing.T, json string) (*Account, *Report, error) {
	t.Helper()
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	return Parse(strings.NewReader(json), cat)
}

func TestWholeAccount(t *testing.T) {
	acc, rep, err := parse(t, `{
	  "format": "GOOD", "version": 3, "source": "Genshin Optimizer",
	  "characters": [
	    {"key": "Skirk", "level": 90, "constellation": 0, "ascension": 6, "talent": {"auto": 10, "skill": 10, "burst": 10}},
	    {"key": "TravelerAnemo", "level": 80, "constellation": 6, "ascension": 6, "talent": {"auto": 1, "skill": 1, "burst": 1}}
	  ],
	  "weapons": [
	    {"key": "FavoniusLance", "level": 90, "ascension": 6, "refinement": 5, "location": "Iansan", "lock": true},
	    {"key": "FavoniusLance", "level": 1, "ascension": 0, "refinement": 5, "location": "", "lock": false}
	  ],
	  "artifacts": [
	    {"setKey": "NoblesseOblige", "slotKey": "sands", "rarity": 5, "level": 20, "mainStatKey": "enerRech_",
	     "location": "Escoffier", "lock": true,
	     "substats": [{"key": "critRate_", "value": 10.5}, {"key": "critDMG_", "value": 14.0}]}
	  ]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Dropped != 0 {
		t.Fatalf("dropped %d entries: %v", rep.Dropped, rep.Problems)
	}
	if len(acc.Characters) != 2 || acc.Characters[0] != (Character{Key: "skirk", Constellation: 0, Level: 90}) {
		t.Errorf("characters = %+v", acc.Characters)
	}
	if acc.Characters[1].Key != "lumineanemo" || acc.Characters[1].Level != 90 {
		t.Errorf("the Traveler should map to the simulator's key at level 90, got %+v", acc.Characters[1])
	}
	if len(acc.Weapons) != 2 {
		t.Errorf("two copies of a weapon should stay two weapons, got %+v", acc.Weapons)
	}
	if len(acc.Artifacts) != 1 || acc.Artifacts[0].Set != "noblesseoblige" || len(acc.Artifacts[0].Substats) != 2 {
		t.Errorf("artifacts = %+v", acc.Artifacts)
	}
}

func TestLevelsAbove90AreKeptOnlyWhenReached(t *testing.T) {
	acc, _, err := parse(t, `{"format": "GOOD", "characters": [
	  {"key": "Furina", "level": 1, "constellation": 0},
	  {"key": "Bennett", "level": 95, "constellation": 6},
	  {"key": "Xiangling", "level": 100, "constellation": 6}]}`)
	if err != nil {
		t.Fatal(err)
	}
	got := []int{acc.Characters[0].Level, acc.Characters[1].Level, acc.Characters[2].Level}
	if got[0] != 90 || got[1] != 95 || got[2] != 100 {
		t.Errorf("levels = %v, want [90 95 100]", got)
	}
}

func TestAnArtifactOnlyFileIsAccepted(t *testing.T) {
	acc, rep, err := parse(t, `{"format": "GOOD", "source": "Artiscan", "version": 1, "artifacts": [
	  {"setKey": "ViridescentVenerer", "slotKey": "flower", "rarity": 5, "level": 20, "mainStatKey": "hp",
	   "substats": [{"key": "eleMas", "value": 44}], "location": "", "lock": false}]}`)
	if err != nil || rep.Dropped != 0 {
		t.Fatalf("err %v, dropped %d", err, rep.Dropped)
	}
	if len(acc.Characters) != 0 || len(acc.Weapons) != 0 || len(acc.Artifacts) != 1 {
		t.Errorf("account = %+v", acc)
	}
}

func TestBadEntriesAreDroppedAndReported(t *testing.T) {
	acc, rep, err := parse(t, `{"format": "GOOD",
	  "characters": [
	    {"key": "NotACharacter", "level": 90, "constellation": 0},
	    {"key": "Danica", "level": 90, "constellation": 0},
	    {"key": "Furina", "level": 90, "constellation": 9},
	    {"key": "Mona", "level": 90, "constellation": 4},
	    {"key": "Mona", "level": 90, "constellation": 4}
	  ],
	  "weapons": [{"key": "FavoniusLance", "refinement": 6}, {"key": "Nonsense", "refinement": 1}],
	  "artifacts": [
	    {"setKey": "FakeSet", "slotKey": "flower", "rarity": 5, "level": 20, "mainStatKey": "hp", "substats": []},
	    {"setKey": "NoblesseOblige", "slotKey": "hat", "rarity": 5, "level": 20, "mainStatKey": "hp", "substats": []},
	    {"setKey": "NoblesseOblige", "slotKey": "flower", "rarity": 5, "level": 21, "mainStatKey": "hp", "substats": []},
	    {"setKey": "NoblesseOblige", "slotKey": "flower", "rarity": 5, "level": 20, "mainStatKey": "luck", "substats": []},
	    {"setKey": "NoblesseOblige", "slotKey": "flower", "rarity": 5, "level": 20, "mainStatKey": "hp",
	     "substats": [{"key": "critRate_; rm -rf", "value": 1}]}
	  ]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(acc.Characters) != 1 || acc.Characters[0].Key != "mona" {
		t.Errorf("only one Mona should survive, got %+v", acc.Characters)
	}
	if len(acc.Weapons) != 0 || len(acc.Artifacts) != 0 {
		t.Errorf("no weapon or artifact should survive, got %+v %+v", acc.Weapons, acc.Artifacts)
	}
	if rep.Dropped != 11 {
		t.Errorf("dropped %d, want 11: %v", rep.Dropped, rep.Problems)
	}
	if !strings.Contains(strings.Join(rep.Problems, "\n"), "not released yet") {
		t.Errorf("an unreleased character should be reported as such: %v", rep.Problems)
	}
}

func TestTextFromTheFileIsCleanedBeforeItIsShown(t *testing.T) {
	_, rep, err := parse(t, `{"format": "GOOD", "weapons": [{"key": "x\"; active furina;\n<script>", "refinement": 1}]}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{";", "\n", "<", "\\"} {
		if strings.Contains(strings.Join(rep.Problems, ""), bad) {
			t.Errorf("problem text still contains %q: %v", bad, rep.Problems)
		}
	}
}

func TestFilesThatCannotBeUsed(t *testing.T) {
	cases := map[string]string{
		"not JSON":        `hello`,
		"not a GOOD file": `{"format": "something else"}`,
		"too many":        `{"format": "GOOD", "characters": [` + strings.Repeat(`{"key":"Mona"},`, MaxCharacters) + `{"key":"Mona"}]}`,
		"too large":       `{"format": "GOOD", "source": "` + strings.Repeat("a", MaxBytes) + `"}`,
	}
	for name, body := range cases {
		if _, _, err := parse(t, body); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestAssumeFourStars(t *testing.T) {
	acc, _, err := parse(t, `{
	  "format": "GOOD", "version": 3, "source": "test",
	  "characters": [],
	  "weapons": [
	    {"key": "FavoniusLance", "level": 90, "ascension": 6, "refinement": 5, "location": "", "lock": false},
	    {"key": "AquilaFavonia", "level": 90, "ascension": 6, "refinement": 1, "location": "", "lock": false}
	  ],
	  "artifacts": []
	}`)
	if err != nil {
		t.Fatal(err)
	}
	cat, _ := catalog.Load()
	fourStars := 0
	for _, w := range cat.Weapons {
		if w.Rarity == 4 {
			fourStars++
		}
	}
	added := acc.AssumeFourStars(cat)
	if added != fourStars-1 {
		t.Errorf("added %d weapons, want %d (every 4-star but the Favonius Lance already listed)", added, fourStars-1)
	}
	refinements := map[string]int{}
	for _, w := range acc.Weapons {
		if r, seen := refinements[w.Key]; seen {
			t.Errorf("%s listed twice (refinements %d and %d)", w.Key, r, w.Refinement)
		}
		refinements[w.Key] = w.Refinement
	}
	if refinements["favoniuslance"] != 5 {
		t.Errorf("the account's own Favonius Lance should keep refinement 5, got %d", refinements["favoniuslance"])
	}
	if refinements["sacrificialsword"] != 1 {
		t.Errorf("an unlisted 4-star should be added at refinement 1, got %d", refinements["sacrificialsword"])
	}
	if refinements["aquilafavonia"] != 1 || len(refinements) != fourStars+1 {
		t.Errorf("5-star weapons should be left as they are and none added: %d weapons", len(refinements))
	}
}
