// Package rotation turns a team rotation into a simulator config for one
// player's version of that team.
//
// A rotation file is a complete KQM Sim config: four characters with example
// builds, a target, and the script of actions to perform. The script is the
// part worth keeping. Parse separates it from the example builds, and Config
// writes it back out with a player's own constellations, levels and weapons.
package rotation

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
)

// Member is one of the team's characters as the rotation file describes it.
type Member struct {
	Name         string // the name the script uses, which may be a short name
	Key          string // the catalog key
	Weapon       string // catalog key of the example weapon
	params       string // character settings the rotation relies on, e.g. starting energy
	weaponParams string
	gear         []string // the example artifact set and stat lines, kept as written
}

// Rotation is a parsed rotation file.
type Rotation struct {
	Members []Member
	rest    []string // every line that is not part of a character's build
}

// Slot is what one player brings for one team member.
type Slot struct {
	Constellation int
	Level         int    // 90, 95 or 100
	Weapon        string // catalog key
	Refinement    int
}

var (
	// "skirk char lvl=90/90 cons=0 talent=9,9,9 +params=[...];"
	charLine = regexp.MustCompile(`^\s*(\w+)\s+char\s([^;]*);`)
	// "skirk add weapon="azurelight" refine=1 lvl=90/90;" and the set and stats forms
	addLine    = regexp.MustCompile(`^\s*(\w+)\s+add\s+(weapon|set|stats)\b([^;]*);`)
	weaponName = regexp.MustCompile(`weapon=\s*"(\w+)"`)
	paramsPart = regexp.MustCompile(`\+params=\[[^\]]*\]`)
)

// Parse reads a rotation file. Every character and weapon it names must be in
// the catalog, so a rotation written for a newer simulator is refused here and
// not halfway through a run.
func Parse(text string, cat *catalog.Catalog) (*Rotation, error) {
	r := &Rotation{}
	index := map[string]int{} // catalog key -> position in r.Members
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
			r.rest = append(r.rest, line) // comments stay, including commented-out builds
			continue
		}
		if m := charLine.FindStringSubmatch(line); m != nil {
			ch, ok := cat.ResolveCharacter(m[1])
			if !ok {
				return nil, fmt.Errorf("rotation uses character %q, which the simulator does not support", m[1])
			}
			if _, dup := index[ch.Key]; dup {
				return nil, fmt.Errorf("rotation defines character %q twice", m[1])
			}
			index[ch.Key] = len(r.Members)
			r.Members = append(r.Members, Member{Name: m[1], Key: ch.Key, params: paramsPart.FindString(m[2])})
			continue
		}
		if m := addLine.FindStringSubmatch(line); m != nil {
			// A file may define "yaemiko" and then give gear to "yae", so gear is
			// matched to its character by catalog key, not by the name written.
			owner, known := cat.ResolveCharacter(m[1])
			i, ok := index[owner.Key]
			if !known || !ok {
				return nil, fmt.Errorf("rotation gives gear to %q before defining that character", m[1])
			}
			member := &r.Members[i]
			if m[2] != "weapon" {
				member.gear = append(member.gear, strings.TrimSpace(line))
				continue
			}
			w := weaponName.FindStringSubmatch(m[0])
			if w == nil {
				return nil, fmt.Errorf("cannot read the weapon line for %q", m[1])
			}
			weapon, ok := cat.ResolveWeapon(w[1])
			if !ok {
				return nil, fmt.Errorf("rotation uses weapon %q, which the simulator does not support", w[1])
			}
			member.Weapon, member.weaponParams = weapon.Key, paramsPart.FindString(m[3])
			continue
		}
		r.rest = append(r.rest, line)
	}
	if len(r.Members) != 4 {
		return nil, fmt.Errorf("rotation has %d characters, want 4", len(r.Members))
	}
	for _, m := range r.Members {
		if m.Weapon == "" {
			return nil, fmt.Errorf("rotation gives %q no weapon", m.Name)
		}
	}
	return r, nil
}

// Team returns the four catalog keys, in the rotation's order.
func (r *Rotation) Team() []string {
	keys := make([]string, len(r.Members))
	for i, m := range r.Members {
		keys[i] = m.Key
	}
	return keys
}

// Config writes the simulator config for a player's version of the team.
// loadout is keyed by catalog key and must cover every member.
//
// Levels and talents follow the optimizer's rule: the character's level is
// given by the slot, talents are always 10 (the simulator adds the +3 from
// constellations itself), and weapons are level 90. The rotation's artifact
// sets and main stats are kept; substats are filled in later by the
// simulator's substat optimizer.
func (r *Rotation) Config(loadout map[string]Slot) (string, error) {
	var b strings.Builder
	for _, m := range r.Members {
		s, ok := loadout[m.Key]
		if !ok {
			return "", fmt.Errorf("no loadout for %s", m.Key)
		}
		if s.Constellation < 0 || s.Constellation > 6 || s.Refinement < 1 || s.Refinement > 5 ||
			(s.Level != 90 && s.Level != 95 && s.Level != 100) || !plainKey.MatchString(s.Weapon) {
			return "", fmt.Errorf("loadout for %s is out of range: %+v", m.Key, s)
		}
		fmt.Fprintf(&b, "%s char lvl=%d/%d cons=%d talent=10,10,10", m.Name, s.Level, s.Level, s.Constellation)
		if m.params != "" {
			b.WriteString(" " + m.params)
		}
		b.WriteString(";\n")
		fmt.Fprintf(&b, "%s add weapon=%q refine=%d lvl=90/90", m.Name, s.Weapon, s.Refinement)
		// Weapon settings such as starting stacks only make sense for the weapon
		// they were written for.
		if m.weaponParams != "" && s.Weapon == m.Weapon {
			b.WriteString(" " + m.weaponParams)
		}
		b.WriteString(";\n")
		for _, g := range m.gear {
			b.WriteString(g + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(strings.TrimLeft(strings.Join(r.rest, "\n"), "\n"))
	return b.String(), nil
}

// plainKey is what a catalog key looks like. Config refuses anything else, so
// that no text other than a known key can ever be written into a config.
var plainKey = regexp.MustCompile(`^[a-z0-9]+$`)
