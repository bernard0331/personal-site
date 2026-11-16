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
}

// Catalog is the full list, with lookups by key.
type Catalog struct {
	Engine       Engine      `json:"engine"`
	Characters   []Character `json:"characters"`
	Weapons      []Weapon    `json:"weapons"`
	ArtifactSets []string    `json:"artifact_sets"`

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
	c.weapons = make(map[string]Weapon, len(c.Weapons))
	for _, w := range c.Weapons {
		c.weapons[w.Key] = w
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
