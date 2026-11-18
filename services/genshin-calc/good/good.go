// Package good reads a player's account from a GOOD file.
//
// GOOD (Genshin Open Object Description) is the JSON format that scanners and
// Genshin Optimizer export. One file describes a whole account with three
// optional lists: characters, weapons and artifacts.
//
// The file comes from an anonymous visitor, so nothing in it is trusted. Every
// key is checked against the catalog, every number against its allowed range,
// and anything that fails is dropped and reported. Only checked values ever
// reach a simulator config.
package good

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
)

// Limits on what one upload may contain. They are far above any real account
// and exist so that a hostile file cannot make the service do unbounded work.
const (
	MaxBytes      = 8 << 20 // 8 MB
	MaxCharacters = 400
	MaxWeapons    = 3000
	MaxArtifacts  = 5000
	maxProblems   = 50 // problems kept for the report; the rest are only counted
)

// Character is one owned character. Level is 90 unless the player has raised
// the character to 95 or 100: levels and talents below the maximum are assumed
// to be reachable, so the file's talents are not kept at all.
type Character struct {
	Key           string // simulator key
	Constellation int    // 0 to 6
	Level         int    // 90, 95 or 100
}

// Weapon is one owned copy of a weapon, always treated as level 90.
type Weapon struct {
	Key        string // simulator key
	Refinement int    // 1 to 5
}

// Substat is one substat line on an artifact, in GOOD's units: percentages are
// written as 5.8, not 0.058.
type Substat struct {
	Key   string  `json:"key"`
	Value float64 `json:"value"`
}

// Artifact is one artifact, kept as the file describes it.
type Artifact struct {
	Set      string // simulator key
	Slot     string // flower, plume, sands, goblet or circlet
	Rarity   int
	Level    int
	MainStat string
	Substats []Substat
}

// Account is everything read from one file.
type Account struct {
	Source     string
	Characters []Character
	Weapons    []Weapon
	Artifacts  []Artifact

	// Unlimited lists weapons the player is assumed to have as many copies of
	// as needed, so the rule that no weapon copy is used twice does not apply
	// to them. Set by AssumeThreeStars.
	Unlimited map[string]bool
}

// AssumeFourStars adds refinement 1 of every 4-star weapon the simulator
// supports that the account does not already list, and returns how many it
// added. It is for players who own (nearly) every 4-star weapon but whose
// scan leaves many out; copies the account does list keep their refinement.
func (a *Account) AssumeFourStars(cat *catalog.Catalog) int {
	listed := map[string]bool{}
	for _, w := range a.Weapons {
		listed[w.Key] = true
	}
	added := 0
	for _, w := range cat.Weapons {
		if w.Rarity == 4 && !listed[w.Key] {
			a.Weapons = append(a.Weapons, Weapon{Key: w.Key, Refinement: 1})
			added++
		}
	}
	return added
}

// AssumeThreeStars treats every 3-star weapon the simulator supports, and every
// craftable 4-star (forged at a blacksmith), as owned at refinement 5 in
// unlimited copies: they are cheap to get and refine, so a player can give one
// to every character who wants it. Listed copies are raised to refinement 5.
// It returns how many weapons it added to the list.
func (a *Account) AssumeThreeStars(cat *catalog.Catalog) int {
	if a.Unlimited == nil {
		a.Unlimited = map[string]bool{}
	}
	unlimited := func(w catalog.Weapon) bool { return w.Rarity == 3 || w.Craftable }
	threeStar := map[string]bool{}
	for _, w := range cat.Weapons {
		if unlimited(w) {
			threeStar[w.Key] = true
			a.Unlimited[w.Key] = true
		}
	}
	listed := map[string]bool{}
	for i, w := range a.Weapons {
		if threeStar[w.Key] {
			a.Weapons[i].Refinement = 5
			listed[w.Key] = true
		}
	}
	added := 0
	for _, w := range cat.Weapons {
		if unlimited(w) && !listed[w.Key] {
			a.Weapons = append(a.Weapons, Weapon{Key: w.Key, Refinement: 5})
			added++
		}
	}
	return added
}

// Report says what was dropped while reading, so the visitor can be told.
type Report struct {
	Problems []string // the first few problems, in file order
	Dropped  int      // how many entries were dropped in total
}

func (r *Report) drop(format string, args ...any) {
	r.Dropped++
	if len(r.Problems) < maxProblems {
		r.Problems = append(r.Problems, fmt.Sprintf(format, args...))
	}
}

// The file's shape. Unknown fields are ignored, which keeps newer GOOD versions
// readable.
type file struct {
	Format     string `json:"format"`
	Source     string `json:"source"`
	Characters []struct {
		Key           string `json:"key"`
		Level         int    `json:"level"`
		Constellation int    `json:"constellation"`
	} `json:"characters"`
	Weapons []struct {
		Key        string `json:"key"`
		Refinement int    `json:"refinement"`
	} `json:"weapons"`
	Artifacts []struct {
		SetKey      string    `json:"setKey"`
		SlotKey     string    `json:"slotKey"`
		Level       int       `json:"level"`
		Rarity      int       `json:"rarity"`
		MainStatKey string    `json:"mainStatKey"`
		Substats    []Substat `json:"substats"`
	} `json:"artifacts"`
}

var slots = map[string]bool{"flower": true, "plume": true, "sands": true, "goblet": true, "circlet": true}

// Every stat key GOOD allows on an artifact.
var stats = map[string]bool{
	"hp": true, "hp_": true, "atk": true, "atk_": true, "def": true, "def_": true,
	"eleMas": true, "enerRech_": true, "heal_": true, "critRate_": true, "critDMG_": true,
	"physical_dmg_": true, "anemo_dmg_": true, "geo_dmg_": true, "electro_dmg_": true,
	"hydro_dmg_": true, "pyro_dmg_": true, "cryo_dmg_": true, "dendro_dmg_": true,
}

// Parse reads a GOOD file. It returns an error only when the file as a whole
// cannot be used; bad entries inside a usable file are dropped and reported.
func Parse(r io.Reader, cat *catalog.Catalog) (*Account, *Report, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("reading the file: %w", err)
	}
	if len(data) > MaxBytes {
		return nil, nil, fmt.Errorf("the file is larger than %d MB", MaxBytes>>20)
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, nil, fmt.Errorf("the file is not valid JSON: %w", err)
	}
	if f.Format != "GOOD" {
		return nil, nil, fmt.Errorf(`the file is not a GOOD file: its "format" is %q`, f.Format)
	}
	if len(f.Characters) > MaxCharacters || len(f.Weapons) > MaxWeapons || len(f.Artifacts) > MaxArtifacts {
		return nil, nil, fmt.Errorf("the file lists more characters, weapons or artifacts than any account can hold")
	}

	acc := &Account{Source: clean(f.Source)}
	rep := &Report{}
	seen := map[string]bool{}

	for _, c := range f.Characters {
		key := catalog.SimKey(c.Key)
		entry, ok := cat.Character(key)
		switch {
		case !ok:
			rep.drop("character %q is not supported by the simulator", clean(c.Key))
		case !entry.Released:
			rep.drop("character %q is not released yet", clean(c.Key))
		case c.Constellation < 0 || c.Constellation > 6:
			rep.drop("character %q has constellation %d", clean(c.Key), c.Constellation)
		case seen[key]:
			rep.drop("character %q is listed twice", clean(c.Key))
		default:
			seen[key] = true
			acc.Characters = append(acc.Characters, Character{Key: key, Constellation: c.Constellation, Level: level(c.Level)})
		}
	}
	for _, w := range f.Weapons {
		key := catalog.SimKey(w.Key)
		switch _, ok := cat.Weapon(key); {
		case !ok:
			rep.drop("weapon %q is not supported by the simulator", clean(w.Key))
		case w.Refinement < 1 || w.Refinement > 5:
			rep.drop("weapon %q has refinement %d", clean(w.Key), w.Refinement)
		default:
			acc.Weapons = append(acc.Weapons, Weapon{Key: key, Refinement: w.Refinement})
		}
	}
	for _, a := range f.Artifacts {
		key := catalog.SimKey(a.SetKey)
		switch {
		case !cat.HasArtifactSet(key):
			rep.drop("artifact set %q is not supported by the simulator", clean(a.SetKey))
		case !slots[a.SlotKey]:
			rep.drop("an artifact of %q has slot %q", clean(a.SetKey), clean(a.SlotKey))
		case a.Rarity < 1 || a.Rarity > 5 || a.Level < 0 || a.Level > 20:
			rep.drop("an artifact of %q has rarity %d and level %d", clean(a.SetKey), a.Rarity, a.Level)
		case !stats[a.MainStatKey]:
			rep.drop("an artifact of %q has main stat %q", clean(a.SetKey), clean(a.MainStatKey))
		case !validSubstats(a.Substats):
			rep.drop("an artifact of %q has substats that are not valid", clean(a.SetKey))
		default:
			acc.Artifacts = append(acc.Artifacts, Artifact{Set: key, Slot: a.SlotKey, Rarity: a.Rarity, Level: a.Level,
				MainStat: a.MainStatKey, Substats: a.Substats})
		}
	}
	return acc, rep, nil
}

// level applies the rule for character levels: everyone is at least 90, and
// only a character the player has actually raised to 95 or 100 goes higher.
func level(fromFile int) int {
	switch {
	case fromFile >= 100:
		return 100
	case fromFile >= 95:
		return 95
	default:
		return 90
	}
}

func validSubstats(subs []Substat) bool {
	if len(subs) > 4 {
		return false
	}
	for _, s := range subs {
		// Scanners write an empty key for a substat slot that is not unlocked yet.
		if s.Key == "" {
			continue
		}
		if !stats[s.Key] || s.Value < 0 || s.Value > 2000 {
			return false
		}
	}
	return true
}

// clean makes text from the file safe to show in a message: short, one line,
// and limited to letters, digits and a few marks.
func clean(s string) string {
	if len(s) > 40 {
		s = s[:40]
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == ' ':
			return r
		}
		return '?'
	}, s)
}
