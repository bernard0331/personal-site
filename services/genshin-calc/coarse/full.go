package coarse

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// This file is the full pass: the most promising teams from the quick pass are
// simulated again, carefully. KQM Sim's substat optimiser chooses each
// member's substats for the player's weapons and constellations (instead of
// keeping the substats the rotation's author chose for theirs), and the
// result is simulated with many more iterations.

// Substat modes for the optimiser, as KQM Sim -options strings.
var SubstatModes = map[string]string{
	// KQM standard: 2 fixed substats per type, 20 liquid, at most 10 in one stat.
	"kqm": "",
	// Top 1% of builds: 1 fixed, 30 liquid, at most 15 in one stat.
	"top1": "total_liquid_substats=30;indiv_liquid_cap=15;fixed_substats_count=1",
}

// Optimiser runs KQM Sim's substat optimiser on a config, then simulates the
// optimised config. It is a Simulator, so the full pass reuses Run and its cache.
type Optimiser struct {
	Bin     *Binary
	Options string // KQM Sim -options for the optimiser; "" for KQM standard
}

// Fingerprint covers the simulator build and the substat mode: the same config
// optimised another way is another result.
func (o *Optimiser) Fingerprint() string {
	if o.Options == "" {
		return o.Bin.Fingerprint() + "+substats"
	}
	return o.Bin.Fingerprint() + "+substats:" + o.Options
}

func (o *Optimiser) Run(ctx context.Context, config string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "full-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, opt := filepath.Join(dir, "config.txt"), filepath.Join(dir, "optimised.txt")
	if err := os.WriteFile(in, []byte(config), 0o600); err != nil {
		return nil, err
	}
	args := []string{"-c", in, "-substatOptim", "-out", opt}
	if o.Options != "" {
		args = append(args, "-options="+o.Options)
	}
	if _, err := o.Bin.run(ctx, dir, args...); err != nil {
		var limit LimitError
		if errors.As(err, &limit) {
			return nil, err
		}
		return nil, fmt.Errorf("substat optimiser: %w", err)
	}
	optimised, err := os.ReadFile(opt)
	if err != nil {
		return nil, err
	}
	return o.Bin.Run(ctx, string(optimised))
}

// statsLines are the "add stats" lines of the config the simulator reports it
// ran: after the optimiser, they hold the chosen substats.
func statsLines(raw []byte) []string {
	var out struct {
		Config string `json:"config_file"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(out.Config, "\n") {
		if f := strings.Fields(l); len(f) >= 3 && f[1] == "add" && f[2] == "stats" {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	return lines
}

// ReadResults reads a results file. When a key appears more than once (a
// failure that was retried), the last line wins.
func ReadResults(path string) ([]Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	index := map[string]int{}
	var out []Result
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var r Result
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.Key == "" {
			continue // a partial last line from a stopped run
		}
		if i, ok := index[r.Key]; ok {
			out[i] = r
			continue
		}
		index[r.Key] = len(out)
		out = append(out, r)
	}
	return out, sc.Err()
}

// MainElement is the element that dealt the most damage. It decides which of
// the seven element groups a team belongs to.
func MainElement(r Result) string {
	best, el := -1.0, ""
	for e, v := range r.ElementDPS {
		if v > best || (v == best && e < el) {
			best, el = v, e
		}
	}
	return el
}

// TeamKey names a team independent of member order.
func TeamKey(team []string) string {
	t := append([]string(nil), team...)
	sort.Strings(t)
	return strings.Join(t, ",")
}

// Picks says how many teams the full pass takes from the quick pass.
type Picks struct {
	Top          int // best teams overall
	PerElement   int // best teams of each main element
	PerCharacter int // best teams each character is in
}

// Select chooses the teams worth simulating carefully. Each team is
// represented by its best rotation; failed results are ignored. A team is
// taken if it is among the best overall, the best of its element, or the best
// for any one of its members. The last rule matters for the solver: no
// character is used twice, so every character needs good teams to choose
// from, not just the characters in the very best teams.
//
// The result is ordered by quick-pass DPS, best first.
func Select(results []Result, p Picks) []Result {
	best := map[string]Result{}
	for _, r := range results {
		if r.Error != "" {
			continue
		}
		k := TeamKey(r.Team)
		if b, ok := best[k]; !ok || r.DPS > b.DPS {
			best[k] = r
		}
	}
	teams := make([]Result, 0, len(best))
	for _, r := range best {
		teams = append(teams, r)
	}
	sort.Slice(teams, func(i, j int) bool {
		if teams[i].DPS != teams[j].DPS {
			return teams[i].DPS > teams[j].DPS
		}
		return TeamKey(teams[i].Team) < TeamKey(teams[j].Team)
	})

	chosen := map[string]bool{}
	perElement, perChar := map[string]int{}, map[string]int{}
	for i, r := range teams {
		take := i < p.Top
		if el := MainElement(r); perElement[el] < p.PerElement {
			perElement[el]++
			take = true
		}
		for _, c := range r.Team {
			if perChar[c] < p.PerCharacter {
				perChar[c]++
				take = true
			}
		}
		if take {
			chosen[TeamKey(r.Team)] = true
		}
	}
	var out []Result
	for _, r := range teams {
		if chosen[TeamKey(r.Team)] {
			out = append(out, r)
		}
	}
	return out
}

// ErrNoResults means the quick pass has nothing for the current simulator build.
var ErrNoResults = errors.New("no quick-pass results for this simulator build; run the coarse pass first")
