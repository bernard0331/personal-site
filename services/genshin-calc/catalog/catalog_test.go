package catalog

import "testing"

func TestLoadHasKnownEntries(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Engine.Release == "" {
		t.Error("catalog does not say which simulator release it came from")
	}
	ayaka, ok := c.Character("kamisatoayaka")
	if !ok || ayaka.Element != "cryo" || ayaka.WeaponType != "sword" || ayaka.Rarity != 5 || !ayaka.Released {
		t.Errorf("kamisatoayaka = %+v, found %v", ayaka, ok)
	}
	lance, ok := c.Weapon("favoniuslance")
	if !ok || lance.WeaponType != "polearm" || lance.Rarity != 4 {
		t.Errorf("favoniuslance = %+v, found %v", lance, ok)
	}
	if !c.HasArtifactSet("viridescentvenerer") {
		t.Error("viridescentvenerer missing")
	}
}

func TestUnreleasedCharactersAreMarked(t *testing.T) {
	c, _ := Load()
	for _, key := range []string{"anastasya", "danica"} {
		ch, ok := c.Character(key)
		if !ok {
			t.Errorf("%s is not in the catalog", key)
		} else if ch.Released {
			t.Errorf("%s should be marked unreleased", key)
		}
	}
}

func TestSimKey(t *testing.T) {
	cases := map[string]string{
		"KamisatoAyaka": "kamisatoayaka",
		"FavoniusLance": "favoniuslance",
		"TravelerAnemo": "lumineanemo",
		"Traveler":      "traveler", // the bare key is only used as a holder of gear
	}
	for in, want := range cases {
		if got := SimKey(in); got != want {
			t.Errorf("SimKey(%q) = %q, want %q", in, got, want)
		}
	}
	c, _ := Load()
	if _, ok := c.Character(SimKey("TravelerDendro")); !ok {
		t.Error("the Traveler's simulator key is not in the catalog")
	}
}
