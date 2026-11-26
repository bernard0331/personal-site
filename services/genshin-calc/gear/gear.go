// Package gear gives characters artifacts from a player's own inventory.
//
// The method (docs/real-artifacts-plan.md): measure, by simulation inside the
// character's team, how much Energy Recharge the character needs, what each
// stat is worth to the team's DPS and what each candidate set layout is
// worth (measure.go); then give every character five pieces at once so no
// piece is worn twice, every ER floor is met and the measured value is
// highest (allocate.go). The numbers shown come from simulating the teams
// on the chosen pieces.
//
// Stats are kept in GOOD's units (percentages as 46.6, not 0.466) and keys
// (critRate_, atk_, eleMas, ...); configs get the simulator's.
package gear

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/bernard0331/personal-site/services/genshin-calc/good"
)

// Stats are stat totals, by GOOD key, in GOOD units.
type Stats map[string]float64

// Add adds o to s.
func (s Stats) Add(o Stats) {
	for k, v := range o {
		s[k] += v
	}
}

// Piece is one artifact from the inventory.
type Piece struct {
	ID       int    // index in the account's artifact list
	Set      string // simulator key
	Slot     string // flower, plume, sands, goblet or circlet
	Main     string // GOOD key of the main stat
	Rarity   int
	Level    int
	Location string // who wears it now ("" for nobody)
	Stats    Stats  // main stat at the piece's level plus substats
}

// Slots in the game's order.
var Slots = []string{"flower", "plume", "sands", "goblet", "circlet"}

//go:embed mainstats.json
var mainStatsJSON []byte

// mainStats[rarity][key][level]: KQM Sim's table of artifact main stats
// (ui/packages/ui/src/data/artifact_main_gen.json), percentages as fractions.
var mainStats map[string]map[string][]float64

func init() {
	if err := json.Unmarshal(mainStatsJSON, &mainStats); err != nil {
		panic(err)
	}
}

// percent reports whether a GOOD stat key is a percentage.
func percent(key string) bool { return strings.HasSuffix(key, "_") }

// MainValue is a main stat's value at a rarity and level, in GOOD units.
func MainValue(rarity, level int, key string) (float64, bool) {
	vals, ok := mainStats[strconv.Itoa(rarity)][key]
	if !ok || level < 0 || level >= len(vals) {
		return 0, false
	}
	if percent(key) {
		return vals[level] * 100, true
	}
	return vals[level], true
}

// Pieces turns an account's artifacts into pieces. Artifacts below 4 stars,
// or with a main stat the table lacks, are left out.
func Pieces(arts []good.Artifact) []Piece {
	var out []Piece
	for i, a := range arts {
		if a.Rarity < 4 {
			continue
		}
		v, ok := MainValue(a.Rarity, a.Level, a.MainStat)
		if !ok {
			continue
		}
		st := Stats{a.MainStat: v}
		for _, s := range a.Substats {
			if s.Key != "" {
				st[s.Key] += s.Value
			}
		}
		out = append(out, Piece{ID: i, Set: a.Set, Slot: a.Slot, Main: a.MainStat, Rarity: a.Rarity, Level: a.Level,
			Location: a.Location, Stats: st})
	}
	return out
}

// simStat names GOOD stat keys in the simulator's config language.
var simStat = map[string]string{
	"hp": "hp", "hp_": "hp%", "atk": "atk", "atk_": "atk%", "def": "def", "def_": "def%",
	"eleMas": "em", "enerRech_": "er", "critRate_": "cr", "critDMG_": "cd", "heal_": "heal",
	"physical_dmg_": "phys%", "pyro_dmg_": "pyro%", "hydro_dmg_": "hydro%", "electro_dmg_": "electro%",
	"cryo_dmg_": "cryo%", "anemo_dmg_": "anemo%", "geo_dmg_": "geo%", "dendro_dmg_": "dendro%",
}

var goodStat = func() map[string]string {
	m := map[string]string{}
	for g, s := range simStat {
		m[s] = g
	}
	return m
}()

// ElementBonus is the GOOD key of an element's damage bonus.
func ElementBonus(element string) string { return strings.ToLower(element) + "_dmg_" }

// StatsLine is a config line giving char the stats s.
func StatsLine(char string, s Stats) string {
	keys := make([]string, 0, len(s))
	for k, v := range s {
		if v != 0 && simStat[k] != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := []string{char, "add stats"}
	for _, k := range keys {
		v := s[k]
		if percent(k) {
			v /= 100
		}
		parts = append(parts, fmt.Sprintf("%s=%s", simStat[k], strconv.FormatFloat(v, 'f', -1, 64)))
	}
	return strings.Join(parts, " ") + ";"
}

func charLine(char string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(char) + `[ \t]+char\b[^\n]*\n`)
}

func gearLines(char string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(char) + `[ \t]+add[ \t]+(set|stats)\b[^\n]*(\n|$)`)
}

// Characters lists a config's characters in order.
func Characters(cfg string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`(?m)^[ \t]*([a-z0-9]+)[ \t]+char\b`).FindAllStringSubmatch(cfg, -1) {
		out = append(out, m[1])
	}
	return out
}

// Strip removes char's artifact lines (sets and stats) from cfg.
func Strip(cfg, char string) string { return gearLines(char).ReplaceAllString(cfg, "") }

// insert puts lines right after char's "char" line.
func insert(cfg, char string, lines []string) (string, error) {
	re := charLine(char)
	loc := re.FindStringIndex(cfg)
	if loc == nil {
		return "", fmt.Errorf("the config has no %s", char)
	}
	return cfg[:loc[1]] + strings.Join(lines, "\n") + "\n" + cfg[loc[1]:], nil
}

// WithGear replaces char's artifacts: these set counts and stat totals.
func WithGear(cfg, char string, sets map[string]int, s Stats) (string, error) {
	cfg = Strip(cfg, char)
	var lines []string
	names := make([]string, 0, len(sets))
	for set := range sets {
		names = append(names, set)
	}
	sort.Strings(names)
	for _, set := range names {
		if n := sets[set]; n >= 2 {
			lines = append(lines, fmt.Sprintf(`%s add set="%s" count=%d;`, char, set, min(n, 4)))
		}
	}
	lines = append(lines, StatsLine(char, s))
	return insert(cfg, char, lines)
}

// AddStats gives char extra stats on top of what cfg gives it.
func AddStats(cfg, char string, s Stats) (string, error) {
	return insert(cfg, char, []string{StatsLine(char, s)})
}

// WithSets replaces char's set lines, keeping its stats.
func WithSets(cfg, char string, sets map[string]int) (string, error) {
	st := ArtifactStats(cfg, char)
	return WithGear(cfg, char, sets, st)
}

var statement = regexp.MustCompile(`(?m)^[ \t]*([a-z0-9]+)[ \t]+add[ \t]+(set|stats)\b[ \t]*([^;\n]*);`)

// ArtifactStats totals char's "add stats" lines in cfg (GOOD units).
// Values may be written as "0.0662*10".
func ArtifactStats(cfg, char string) Stats {
	out := Stats{}
	for _, m := range statement.FindAllStringSubmatch(cfg, -1) {
		if m[1] != char || m[2] != "stats" {
			continue
		}
		for _, f := range strings.Fields(m[3]) {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			g := goodStat[k]
			if g == "" {
				continue
			}
			x := 1.0
			for i, part := range strings.Split(v, "*") {
				n, err := strconv.ParseFloat(part, 64)
				if err != nil {
					x = 0
					break
				}
				if i == 0 {
					x = n
				} else {
					x *= n
				}
			}
			if percent(g) {
				x *= 100
			}
			out[g] += x
		}
	}
	return out
}

// Sets reads char's set lines in cfg: set -> count.
func Sets(cfg, char string) map[string]int {
	out := map[string]int{}
	for _, m := range statement.FindAllStringSubmatch(cfg, -1) {
		if m[1] != char || m[2] != "set" {
			continue
		}
		set, count := "", 0
		for _, f := range strings.Fields("set" + m[3]) { // m[3] starts with ="name"
			k, v, _ := strings.Cut(f, "=")
			switch k {
			case "set":
				set = strings.Trim(v, `"`)
			case "count":
				count, _ = strconv.Atoi(v)
			}
		}
		if set != "" {
			out[set] += count
		}
	}
	return out
}

var optionsLine = regexp.MustCompile(`(?m)^[ \t]*options[ \t]+([^;\n]*);`)

// Options sets the iteration count, and with ignoreEnergy lets every burst
// go off regardless of energy, so ER mixes into no other measurement.
func Options(cfg string, iterations int, ignoreEnergy bool) string {
	m := optionsLine.FindStringSubmatchIndex(cfg)
	var fields []string
	if m != nil {
		for _, f := range strings.Fields(cfg[m[2]:m[3]]) {
			if !strings.HasPrefix(f, "iteration=") && !strings.HasPrefix(f, "ignore_burst_energy=") {
				fields = append(fields, f)
			}
		}
	}
	fields = append(fields, fmt.Sprintf("iteration=%d", iterations))
	if ignoreEnergy {
		fields = append(fields, "ignore_burst_energy=true")
	}
	line := "options " + strings.Join(fields, " ") + ";"
	if m == nil {
		return line + "\n" + cfg
	}
	return cfg[:m[0]] + line + cfg[m[1]:]
}

// setER is Energy Recharge that a set's 2-piece bonus gives (GOOD units).
var setER = map[string]float64{"emblemofseveredfate": 20, "theexile": 20}

// SetER is the ER a set layout's bonuses give.
func SetER(sets map[string]int) float64 {
	er := 0.0
	for set, n := range sets {
		if n >= 2 {
			er += setER[set]
		}
	}
	return er
}
