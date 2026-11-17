package solver

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

//go:embed highs_solve.py
var highsScript []byte

// HiGHS chooses teams with the HiGHS integer programming solver, through the
// helper script highs_solve.py (embedded) and a Python that has highspy. It
// proves its answers best, which the branch and bound in this package cannot
// do in reasonable time once there are thousands of team options.
type HiGHS struct {
	Python    string        // a Python interpreter with highspy installed
	TimeLimit time.Duration // per solve; the answer is then the best found
}

// Result is a set of chosen teams and whether HiGHS proved it best.
type Result struct {
	Teams   []Team
	Optimal bool
}

// BestHiGHS is Best, solved by HiGHS.
func (p *Problem) BestHiGHS(h *HiGHS, n int, o Objective) (Result, error) {
	return p.solveHiGHS(h, n, o, nil)
}

// ByElementHiGHS is ByElement, solved by HiGHS: the result is in the order of
// elements, with a zero Team for an element that has none.
func (p *Problem) ByElementHiGHS(h *HiGHS, elements []string, o Objective) (Result, error) {
	r, err := p.solveHiGHS(h, len(elements), o, elements)
	if err != nil {
		return r, err
	}
	out := make([]Team, len(elements))
	for _, t := range r.Teams {
		for i, e := range elements {
			if t.Element == e {
				out[i] = t
			}
		}
	}
	r.Teams = out
	return r, nil
}

func (p *Problem) solveHiGHS(h *HiGHS, n int, o Objective, elements []string) (Result, error) {
	type option struct {
		DPS  float64  `json:"dps"`
		Uses []string `json:"uses"`
	}
	in := struct {
		Options   []option       `json:"options"`
		Capacity  map[string]int `json:"capacity"`
		K         int            `json:"k"`
		Objective string         `json:"objective"`
		TimeLimit float64        `json:"time_limit"`
	}{Capacity: map[string]int{}, K: n, Objective: "sum", TimeLimit: h.TimeLimit.Seconds()}
	if o == Min {
		in.Objective = "min"
	}
	if in.TimeLimit <= 0 {
		in.TimeLimit = 1e6
	}
	want := map[string]bool{}
	for _, e := range elements {
		want[e] = true
	}
	var teams []Team
	for i := range p.Teams {
		t := &p.Teams[i]
		if elements != nil && !want[t.Element] {
			continue
		}
		uses := p.resources(t)
		for _, r := range uses {
			in.Capacity[r] = p.capacity(r)
		}
		if elements != nil {
			uses = append(uses, elementResource+t.Element)
			in.Capacity[elementResource+t.Element] = 1
		}
		in.Options = append(in.Options, option{DPS: t.DPS, Uses: uses})
		teams = append(teams, *t)
	}
	body, err := json.Marshal(in)
	if err != nil {
		return Result{}, err
	}

	dir, err := os.MkdirTemp("", "highs-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)
	script := filepath.Join(dir, "highs_solve.py")
	if err := os.WriteFile(script, highsScript, 0o600); err != nil {
		return Result{}, err
	}
	// The script's own limit applies to each of its up to three solves.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Duration(in.TimeLimit)*time.Second+time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.Python, script)
	cmd.Stdin = bytes.NewReader(body)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return Result{}, fmt.Errorf("HiGHS helper: %v: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	var out struct {
		Chosen  []int `json:"chosen"`
		Optimal bool  `json:"optimal"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return Result{}, fmt.Errorf("HiGHS helper output: %w", err)
	}
	var r Result
	for _, i := range out.Chosen {
		if i < 0 || i >= len(teams) {
			return Result{}, fmt.Errorf("HiGHS helper chose option %d of %d", i, len(teams))
		}
		r.Teams = append(r.Teams, teams[i])
	}
	r.Teams = sorted(r.Teams)
	r.Optimal = out.Optimal
	return r, nil
}
