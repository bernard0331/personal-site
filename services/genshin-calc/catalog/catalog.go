// Package catalog lists every character, weapon and artifact set the pinned
// KQM Sim release supports. The data is generated from the simulator's source by
// tools/gen_catalog.py and embedded in the program, so the service needs no
// simulator source at build time.
package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed catalog.json
var raw []byte

// craftable.txt lists the weapons that can be forged, one key per line.
//
//go:embed craftable.txt
var craftableList string

// Engine identifies the simulator release the catalog was generated from.
type Engine struct {
	Name    string `json:"name"`
	Release string `json:"release"`
	Commit  string `json:"commit"`
}

// Character is one playable character as the simulator knows it.
type Character struct {
	Key        string `json:"key"`         // simulator key, e.g. "kamisatoayaka"
	Element    string `json:"element"`     // pyro, hydro, electro, cryo, anemo, geo or dendro
	WeaponType string `json:"weapon_type"` // sword, claymore, polearm, bow or catalyst
	Rarity     int    `json:"rarity"`
	Released   bool   `json:"released"` // false for characters not yet in the game
}

// Weapon is one weapon as the simulator knows it.
type Weapon struct {
	Key        string `json:"key"`
	WeaponType string `json:"weapon_type"`
	Rarity     int    `json:"rarity"`
	// Craftable 4-star weapons can be forged as often as a player likes, so
	// they are treated like 3-stars: unlimited copies at R5. From craftable.txt.
	Craftable bool `json:"craftable,omitempty"`
}

// Catalog is the full list, with lookups by key.
type Catalog struct {
	Engine       Engine      `json:"engine"`
	Characters   []Character `json:"characters"`
	Weapons      []Weapon    `json:"weapons"`
	ArtifactSets []string    `json:"artifact_sets"`
	// Shortcuts are the short names rotation scripts may use, such as "bina"
	// for columbina or "favlance" for favoniuslance.
	Shortcuts struct {
		Characters   map[string]string `json:"characters"`
		Weapons      map[string]string `json:"weapons"`
		ArtifactSets map[string]string `json:"artifact_sets"`
	} `json:"shortcuts"`

	characters map[string]Character
	weapons    map[string]Weapon
	sets       map[string]bool
}

// Load parses the embedded catalog. It fails only if the embedded file is broken.
func Load() (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	c.characters = make(map[string]Character, len(c.Characters))
	for _, ch := range c.Characters {
		c.characters[ch.Key] = ch
	}
	craftable := map[string]bool{}
	for _, line := range strings.Split(craftableList, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			craftable[line] = true
		}
	}
	c.weapons = make(map[string]Weapon, len(c.Weapons))
	for i, w := range c.Weapons {
		if craftable[w.Key] {
			c.Weapons[i].Craftable = true
			w.Craftable = true
			delete(craftable, w.Key)
		}
		c.weapons[w.Key] = w
	}
	for k := range craftable {
		return nil, fmt.Errorf("catalog: craftable.txt names %q, which the simulator does not have", k)
	}
	c.sets = make(map[string]bool, len(c.ArtifactSets))
	for _, s := range c.ArtifactSets {
		c.sets[s] = true
	}
	return &c, nil
}

// SimKey turns a GOOD key such as "KamisatoAyaka" into the simulator's form,
// "kamisatoayaka". The simulator's keys are the GOOD keys in lower case. The one
// exception is the Traveler: GOOD says "TravelerAnemo", and the simulator has a
// key for each twin, so "traveleranemo" becomes "lumineanemo". The twins have the
// same stats, so which one is used does not change any result.
func SimKey(goodKey string) string {
	key := strings.ToLower(goodKey)
	if element, ok := strings.CutPrefix(key, "traveler"); ok && element != "" {
		return "lumine" + element
	}
	return key
}

// Character looks a character up by simulator key.
func (c *Catalog) Character(key string) (Character, bool) {
	ch, ok := c.characters[key]
	return ch, ok
}

// Weapon looks a weapon up by simulator key.
func (c *Catalog) Weapon(key string) (Weapon, bool) {
	w, ok := c.weapons[key]
	return w, ok
}

// HasArtifactSet reports whether the simulator supports an artifact set.
func (c *Catalog) HasArtifactSet(key string) bool { return c.sets[key] }

// ResolveCharacter finds a character by the name a rotation script uses for it:
// the full key or one of the simulator's short names. The Traveler's twins have
// the same stats, so an "aether" name is treated as its "lumine" twin.
func (c *Catalog) ResolveCharacter(name string) (Character, bool) {
	key := strings.ToLower(name)
	if full, ok := c.Shortcuts.Characters[key]; ok {
		key = full
	}
	if element, ok := strings.CutPrefix(key, "aether"); ok {
		key = "lumine" + element
	}
	return c.Character(key)
}

// ResolveWeapon finds a weapon by full key or short name.
func (c *Catalog) ResolveWeapon(name string) (Weapon, bool) {
	key := strings.ToLower(name)
	if full, ok := c.Shortcuts.Weapons[key]; ok {
		key = full
	}
	return c.Weapon(key)
}
