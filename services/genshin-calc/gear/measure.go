package gear

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/bernard0331/personal-site/services/genshin-calc/coarse"
)

// SetNeed asks for Count pieces of a set (or of any set in a group).
type SetNeed struct {
	Set   string // simulator key, or a group name from Groups
	Count int
}

// Layout is a set layout worth trying, e.g. 4 Golden Troupe, or 2 Emblem +
// 2 of any 18% ATK set.
type Layout struct {
	Name string
	Need []SetNeed
}

// Groups are sets whose 2-piece bonus is the same, so a 2+2 layout can
// take either.
var Groups = map[string][]string{
	"atk18": {"gladiatorsfinale", "shimenawasreminiscence", "echoesofanoffering", "vermillionhereafter",
		"fragmentofharmonicwhimsy", "unfinishedreverie"},
}

// Matches reports whether a piece of set counts toward need.
func (n SetNeed) Matches(set string) bool {
	if n.Set == set {
		return true
	}
	for _, s := range Groups[n.Set] {
		if s == set {
			return true
		}
	}
	return false
}

// sample is the concrete sets a layout is simulated with.
func (l Layout) sample() map[string]int {
	out := map[string]int{}
	for _, n := range l.Need {
		set := n.Set
		if g := Groups[set]; len(g) > 0 {
			set = g[0]
		}
		out[set] += n.Count
	}
	return out
}

// Character is one character to gear, and the layouts to try on it.
type Character struct {
	Key     string // simulator key, as in the config
	Element string // pyro, hydro, ...
	Layouts []Layout
}

// Plan is the measuring simulations for one team: run Jobs (with the seeded
// simulator, all on the same seed), then Analyze their results.
type Plan struct {
	Config string // the team on its standard artifacts
	Chars  []Character
	Jobs   []coarse.Job
	order  []string // the config's characters, in order (results' per-member fields)
	er0    map[string]float64
}

// steps is how much of each stat a measurement adds. With every run on the
// same random rolls, the differences are the stat alone.
var steps = Stats{"atk_": 10, "atk": 100, "hp_": 10, "hp": 1000, "def_": 10, "def": 100,
	"critRate_": 5, "critDMG_": 10, "eleMas": 50, "enerRech_": 10}

const elementStep = 10

// erStep and erRange bound the Energy Recharge sweep (GOOD %).
const (
	erStep = 5.0
	erMax  = 120.0
	// A character waiting under half a second per fight in all is not waiting.
	waitOK = 0.5
)

// NewPlan lists the measurements: stat values and layouts with energy
// ignored (iterations each), and an ER sweep with energy on (erIterations).
func NewPlan(cfg string, chars []Character, iterations, erIterations int) (*Plan, error) {
	p := &Plan{Config: cfg, Chars: chars, order: Characters(cfg), er0: map[string]float64{}}
	team := p.order
	add := func(id, c string) {
		p.Jobs = append(p.Jobs, coarse.Job{ID: id, Source: "gear", Team: team, Config: c})
	}
	ignored := Options(cfg, iterations, true)
	add("base", ignored)
	for _, ch := range chars {
		if !contains(p.order, ch.Key) {
			return nil, fmt.Errorf("the config has no %s", ch.Key)
		}
		st := Stats{}
		for k, v := range steps {
			st[k] = v
		}
		st[ElementBonus(ch.Element)] = elementStep
		for k, v := range st {
			c, err := AddStats(ignored, ch.Key, Stats{k: v})
			if err != nil {
				return nil, err
			}
			add("w/"+ch.Key+"/"+k, c)
		}
		// "none" (no set bonus) is always possible, whatever the inventory.
		for _, l := range append([]Layout{{Name: "none"}}, ch.Layouts...) {
			c, err := WithSets(ignored, ch.Key, l.sample())
			if err != nil {
				return nil, err
			}
			add("l/"+ch.Key+"/"+l.Name, c)
		}
		// ER sweep, relative to what the standard artifacts give: from
		// none at all up to erMax more.
		own := ArtifactStats(cfg, ch.Key)["enerRech_"] + SetER(Sets(cfg, ch.Key))
		p.er0[ch.Key] = own
		on := Options(cfg, erIterations, false)
		for o := -math.Floor(own/erStep) * erStep; o <= erMax; o += erStep {
			c, err := AddStats(on, ch.Key, Stats{"enerRech_": o})
			if err != nil {
				return nil, err
			}
			add(fmt.Sprintf("er/%s/%g", ch.Key, o), c)
		}
	}
	return p, nil
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// LayoutValue is what a layout adds to the team's DPS over the character's
// standard sets (simulated with energy ignored).
type LayoutValue struct {
	Layout
	Value float64
	// ERNeed is the artifact plus set-bonus ER the character needs wearing
	// this layout (a set can change how much energy it gets or needs); 0
	// until ERChecks has run, then the character's own floor if no check
	// point waited less; +Inf if none of them was enough.
	ERNeed float64
}

// Measured is what the measurements found for one character.
type Measured struct {
	Char    string
	Element string
	Weights Stats // team DPS per unit of each stat (GOOD units)
	Layouts []LayoutValue
	ERNeed  float64 // artifact plus set-bonus ER (GOOD %) at which it never waits; +Inf if no amount was enough
	BaseDPS float64 // the team on standard artifacts, energy ignored
	OwnSets map[string]int
	// CritRateOther is the character's CRIT Rate from everything but
	// artifacts (GOOD %); artifacts may add up to 100 minus this.
	CritRateOther float64
}

// Analyze turns the measurements' results (by job ID) into each character's
// measured values.
func (p *Plan) Analyze(res map[string]coarse.Result) ([]Measured, error) {
	base, ok := res["base"]
	if !ok || base.Error != "" {
		return nil, fmt.Errorf("the base measurement failed: %s", base.Error)
	}
	var out []Measured
	for _, ch := range p.Chars {
		m := Measured{Char: ch.Key, Element: ch.Element, Weights: Stats{}, BaseDPS: base.DPS, OwnSets: Sets(p.Config, ch.Key)}
		for i, c := range p.order {
			if c == ch.Key && i < len(base.CritRateOther) {
				m.CritRateOther = base.CritRateOther[i]
			}
		}
		for id, r := range res {
			key, ok := strings.CutPrefix(id, "w/"+ch.Key+"/")
			if !ok || r.Error != "" {
				continue
			}
			step := steps[key]
			if key == ElementBonus(ch.Element) {
				step = elementStep
			}
			m.Weights[key] = (r.DPS - base.DPS) / step
		}
		for _, l := range append([]Layout{{Name: "none"}}, ch.Layouts...) {
			r, ok := res["l/"+ch.Key+"/"+l.Name]
			if !ok || r.Error != "" {
				continue
			}
			m.Layouts = append(m.Layouts, LayoutValue{Layout: l, Value: r.DPS - base.DPS})
		}
		sort.Slice(m.Layouts, func(i, j int) bool { return m.Layouts[i].Value > m.Layouts[j].Value })
		m.ERNeed = p.erNeed(ch.Key, res)
		out = append(out, m)
	}
	return out, nil
}

// erNeed is the least ER above which the character never waits for energy.
func (p *Plan) erNeed(char string, res map[string]coarse.Result) float64 {
	idx := -1
	for i, c := range p.order {
		if c == char {
			idx = i
		}
	}
	type point struct{ er, wait float64 }
	var pts []point
	for id, r := range res {
		v, ok := strings.CutPrefix(id, "er/"+char+"/")
		if !ok || r.Error != "" || idx >= len(r.EnergyWaits) {
			continue
		}
		var o float64
		fmt.Sscanf(v, "%g", &o)
		pts = append(pts, point{p.er0[char] + o, r.EnergyWaits[idx]})
	}
	if len(pts) == 0 {
		return math.Inf(1)
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].er < pts[j].er })
	need := math.Inf(1)
	for i := len(pts) - 1; i >= 0 && pts[i].wait < waitOK; i-- {
		need = pts[i].er
	}
	return math.Max(0, need)
}

// erCheckOffsets are the layout checks' points, relative to the character's
// floor on its own sets (GOOD %).
var erCheckOffsets = []float64{-10, 0, 10, 20, 30, 40}

// ERChecks lists a second batch, after Analyze: each candidate layout with
// energy on at a few ER amounts around the character's own floor, since a set
// can change how much energy a character gets or needs.
func (p *Plan) ERChecks(ms []Measured, erIterations int) ([]coarse.Job, error) {
	on := Options(p.Config, erIterations, false)
	var jobs []coarse.Job
	for _, m := range ms {
		if math.IsInf(m.ERNeed, 1) {
			continue
		}
		have := ArtifactStats(p.Config, m.Char)["enerRech_"]
		for _, l := range m.Layouts {
			sets := l.sample()
			for _, o := range erCheckOffsets {
				target := m.ERNeed + o
				// The config's artifact ER plus the layout's set ER, moved
				// to the target.
				delta := target - have - SetER(sets)
				c, err := WithSets(on, m.Char, sets)
				if err != nil {
					return nil, err
				}
				if c, err = AddStats(c, m.Char, Stats{"enerRech_": delta}); err != nil {
					return nil, err
				}
				jobs = append(jobs, coarse.Job{ID: fmt.Sprintf("erl/%s/%s/%g", m.Char, l.Name, target), Source: "gear", Team: p.order, Config: c})
			}
		}
	}
	return jobs, nil
}

// ApplyERChecks sets each layout's ER need from ERChecks' results.
func (p *Plan) ApplyERChecks(ms []Measured, res map[string]coarse.Result) {
	for mi := range ms {
		m := &ms[mi]
		idx := -1
		for i, c := range p.order {
			if c == m.Char {
				idx = i
			}
		}
		for li := range m.Layouts {
			l := &m.Layouts[li]
			l.ERNeed = math.Inf(1)
			prefix := "erl/" + m.Char + "/" + l.Name + "/"
			type point struct{ er, wait float64 }
			var pts []point
			for id, r := range res {
				v, ok := strings.CutPrefix(id, prefix)
				if !ok || r.Error != "" || idx >= len(r.EnergyWaits) {
					continue
				}
				var er float64
				fmt.Sscanf(v, "%g", &er)
				pts = append(pts, point{er, r.EnergyWaits[idx]})
			}
			sort.Slice(pts, func(i, j int) bool { return pts[i].er < pts[j].er })
			for i := len(pts) - 1; i >= 0 && pts[i].wait < waitOK; i-- {
				l.ERNeed = math.Max(0, pts[i].er)
			}
		}
	}
}
