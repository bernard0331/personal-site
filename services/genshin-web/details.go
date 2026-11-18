package main

import (
	"regexp"
	"strings"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
	"github.com/bernard0331/personal-site/services/genshin-calc/coarse"
)

// Details is what a team card's Details panel shows: where the rotation comes
// from, the rotation itself, the example builds the quick run used (the
// rotation's own; the optimizer keeps them), and how the simulation went.
type Details struct {
	SourceName string             `json:"source_name"`          // e.g. "KQM Sim Database" or "KQM's Zibai quick guide"
	SourceURL  string             `json:"source_url,omitempty"` // a page to read more, when known
	Credit     []string           `json:"credit,omitempty"`     // e.g. the database entry's description and submitter
	Script     []string           `json:"script"`               // the rotation's action lines
	Builds     []BuildView        `json:"builds"`               // in team order
	ElementDPS map[string]float64 `json:"element_dps,omitempty"`
	Duration   float64            `json:"duration"` // simulated fight length, seconds
	Iterations int                `json:"iterations"`
	Waits      map[string]float64 `json:"waits,omitempty"`    // seconds spent waiting, by reason
	RotCons    []int              `json:"rot_cons,omitempty"` // constellations the rotation was written for
}

// BuildView is one member's example build: artifact sets and main stats.
type BuildView struct {
	Key  string         `json:"key"`
	Sets map[string]int `json:"sets,omitempty"` // set key -> pieces
	Main string         `json:"main,omitempty"` // e.g. "ATK% / Cryo DMG / CRIT DMG"
}

const maxScriptLines = 80

var (
	buildLine  = regexp.MustCompile(`^\s*(\w+)\s+(char|add)\b`)
	setLine    = regexp.MustCompile(`add\s+set="?(\w+)"?\s+count=(\d+)`)
	statsLine  = regexp.MustCompile(`add\s+stats\s+([^;]*);`)
	setupLine  = regexp.MustCompile(`^\s*(\w+\s+(char|add)\b|target\b|energy\b|options\b|hurt\b)`)
	sourceLine = regexp.MustCompile(`^#\s*Source:\s*(.*?),?\s*(https?://\S+)\s*$`)
	teamLine   = regexp.MustCompile(`^#\s+Team "(.*)"`)
)

// details builds a team's Details panel from its rotation and simulation.
func (e *engine) details(r coarse.Result) *Details {
	src, ok := e.byRotation[r.Source+"/"+r.ID]
	if !ok {
		return nil
	}
	d := &Details{ElementDPS: r.ElementDPS, Duration: r.Duration, Iterations: r.Iterations, Waits: r.Waits, RotCons: r.RotCons}
	lines := strings.Split(src.Config, "\n")
	switch src.From {
	case "kqm-db":
		d.SourceName = "KQM Sim Database, entry " + src.ID
		if desc := strings.TrimSpace(src.Description); desc != "" {
			d.Credit = append(d.Credit, desc)
		}
		if src.Submitter != "" {
			d.Credit = append(d.Credit, "Submitted by "+src.Submitter)
		}
	default:
		d.SourceName = "Own rotation " + src.ID
		for _, l := range lines {
			if m := sourceLine.FindStringSubmatch(l); m != nil {
				d.SourceName, d.SourceURL = m[1], m[2]
			} else if m := teamLine.FindStringSubmatch(l); m != nil {
				d.Credit = append(d.Credit, `Team "`+m[1]+`"`)
			}
		}
	}
	d.Script = script(lines)
	d.Builds = builds(lines, r.Team, e.cat)
	return d
}

// script is the rotation itself: every line after the last setup line (builds,
// target, energy, options), without comments and blank lines.
func script(lines []string) []string {
	last := -1
	for i, l := range lines {
		if setupLine.MatchString(l) {
			last = i
		}
	}
	var out []string
	for _, l := range lines[last+1:] {
		if i := strings.Index(l, "#"); i >= 0 {
			l = l[:i]
		}
		if strings.TrimSpace(l) == "" {
			continue
		}
		if len(out) == maxScriptLines {
			out = append(out, "…")
			break
		}
		out = append(out, strings.TrimRight(l, " \t"))
	}
	return out
}

// builds reads each member's artifact sets and main stats from the config.
func builds(lines []string, team []string, cat *catalog.Catalog) []BuildView {
	by := map[string]*BuildView{}
	for _, l := range lines {
		m := buildLine.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		ch, ok := cat.ResolveCharacter(m[1])
		if !ok {
			continue
		}
		b := by[ch.Key]
		if b == nil {
			b = &BuildView{Key: ch.Key, Sets: map[string]int{}}
			by[ch.Key] = b
		}
		if s := setLine.FindStringSubmatch(l); s != nil {
			key := strings.ToLower(s[1])
			if full, ok := cat.Shortcuts.ArtifactSets[key]; ok {
				key = full
			}
			n := 0
			for _, c := range s[2] {
				n = n*10 + int(c-'0')
			}
			b.Sets[key] = min(n, 5)
		} else if s := statsLine.FindStringSubmatch(l); s != nil && b.Main == "" {
			b.Main = mainStats(s[1])
		}
	}
	out := make([]BuildView, 0, len(team))
	for _, k := range team {
		if b := by[k]; b != nil {
			out = append(out, *b)
		} else {
			out = append(out, BuildView{Key: k})
		}
	}
	return out
}

var statNames = map[string]string{"atk%": "ATK%", "hp%": "HP%", "def%": "DEF%", "em": "EM", "er": "ER",
	"cr": "CRIT Rate", "cd": "CRIT DMG", "heal": "Healing", "phys%": "Physical DMG"}

// mainStats names a build's sands, goblet and circlet from its main-stat line
// ("hp=4780 atk=311 atk%=0.466 cryo%=0.466 cd=0.622"); the flower's HP and the
// plume's ATK are left out.
func mainStats(line string) string {
	var out []string
	for _, f := range strings.Fields(line) {
		k, _, ok := strings.Cut(f, "=")
		if !ok || k == "hp" || k == "atk" {
			continue
		}
		name, ok := statNames[k]
		if !ok {
			if el, isEl := strings.CutSuffix(k, "%"); isEl {
				name = strings.ToUpper(el[:1]) + el[1:] + " DMG"
			} else {
				name = k
			}
		}
		out = append(out, name)
	}
	return strings.Join(out, " / ")
}
