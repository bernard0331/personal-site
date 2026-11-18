package rotation

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Standard reports whether every member's example build uses KQM-standard
// substats: each substat a whole number of standard rolls (5-star, or 4-star
// for sims built on 4-star artifacts). Database sims built on someone's
// personal artifacts fail this; their numbers say more about those artifacts
// than about the team, so they are not candidates.
//
// Standard configs write the rolls either as "cr=0.0331*10" or already
// multiplied, "cr=0.331"; both forms count.
func (r *Rotation) Standard() bool {
	for _, m := range r.Members {
		ok := false
		for _, line := range m.gear {
			if strings.Contains(line, " add stats") && standardSubstats(line) {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// standardRoll is one average roll of each substat on a 5-star artifact, as
// KQM-standard configs write them.
var standardRoll = map[string]float64{
	"def%": 0.062, "def": 19.68, "hp": 253.94, "hp%": 0.0496, "atk": 16.54,
	"atk%": 0.0496, "er": 0.0551, "em": 19.82, "cr": 0.0331, "cd": 0.0662,
}

// fourStarScale is a 4-star roll as a fraction of a 5-star one.
const fourStarScale = 0.84

var statTerm = regexp.MustCompile(`([a-z%]+)=([0-9.]+)(?:\*([0-9]+))?`)

// standardSubstats reports whether a stats line is a substat line (six or
// more substat kinds; a main-stat line has five entries) whose totals are
// all whole numbers of standard rolls.
func standardSubstats(line string) bool {
	total := map[string]float64{}
	for _, m := range statTerm.FindAllStringSubmatch(line, -1) {
		unit, ok := standardRoll[m[1]]
		if !ok || unit == 0 {
			continue
		}
		v, _ := strconv.ParseFloat(m[2], 64)
		n := 1.0
		if m[3] != "" {
			n, _ = strconv.ParseFloat(m[3], 64)
		}
		total[m[1]] += v * n
	}
	if len(total) < 6 {
		return false
	}
	whole := func(scale float64) bool {
		for k, v := range total {
			rolls := v / (standardRoll[k] * scale)
			if rolls < 1.5 || math.Abs(rolls-math.Round(rolls)) > 0.03 {
				return false
			}
		}
		return true
	}
	return whole(1) || whole(fourStarScale)
}
