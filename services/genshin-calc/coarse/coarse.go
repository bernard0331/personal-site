// Package coarse runs many team simulations and caches their results.
//
// It is the first stage of finding a player's best teams: every rotation the
// player can field is simulated once, cheaply, so the solver has a DPS for each
// team and the most promising ones can be simulated again carefully.
//
// Simulations run in parallel on a pool of worker goroutines. Each result is
// written as one JSON line as soon as it finishes, so a long run can be stopped
// and resumed: results already in the cache are skipped.
package coarse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Job is one simulation to run: one rotation with one player's loadout.
type Job struct {
	ID      string   // rotation id, e.g. a KQM Sim Database id or an own file's name
	Source  string   // where the rotation came from: "kqm-db" or "own"
	Team    []string // the four characters' catalog keys, in the rotation's order
	Weapons []string // what each member wields, e.g. "bennett: mistsplitterreforged R1"
	Config  string   // the complete simulator config
	// KeepStats keeps the config's "add stats" lines in the result: the full
	// pass needs them, because the substat optimiser chose them.
	KeepStats bool
}

// Result is what one simulation produced. It is stored as one JSON line.
type Result struct {
	Key        string             `json:"key"` // cache key: the config and the simulator binary
	ID         string             `json:"id"`
	Source     string             `json:"source"`
	Team       []string           `json:"team"`
	Weapons    []string           `json:"weapons"`
	SimBinary  string             `json:"sim_binary"`  // SHA-256 of the simulator binary, first 12 hex digits
	SimVersion string             `json:"sim_version"` // the commit the simulator reports
	Iterations int                `json:"iterations"`
	DPS        float64            `json:"dps"`
	DPSSD      float64            `json:"dps_sd"`
	CharDPS    []float64          `json:"char_dps"`        // per member, in Team order
	ElementDPS map[string]float64 `json:"element_dps"`     // decides the team's element
	Duration   float64            `json:"duration"`        // simulated fight length in seconds
	Waits      map[string]float64 `json:"waits,omitempty"` // seconds the team spent waiting, by reason
	Flags      []string           `json:"flags,omitempty"` // reasons to distrust the number
	Stats      []string           `json:"stats,omitempty"` // the config's "add stats" lines, when the job asks for them
	Error      string             `json:"error,omitempty"` // set when the simulation failed
	Final      bool               `json:"final,omitempty"` // the failure would repeat (a limit was exceeded): do not retry
}

// Simulator runs one config and returns the simulator's JSON result.
type Simulator interface {
	Run(ctx context.Context, config string) ([]byte, error)
	// Fingerprint identifies the simulator build, so results from another
	// build are not taken from the cache.
	Fingerprint() string
}

// Key is the cache key for a job: the same config on the same simulator build
// gives the same result (up to random variation).
func Key(config, fingerprint string) string {
	sum := sha256.Sum256([]byte(fingerprint + "\n" + config))
	return hex.EncodeToString(sum[:])[:24]
}

var iterationOption = regexp.MustCompile(`iteration\s*=\s*\d+`)

// SetIterations makes a config run n iterations, replacing the rotation's own
// setting or adding one.
func SetIterations(config string, n int) string {
	if iterationOption.MatchString(config) {
		return iterationOption.ReplaceAllString(config, fmt.Sprintf("iteration=%d", n))
	}
	return fmt.Sprintf("options iteration=%d;\n", n) + config
}

// waitThreshold is how long, in seconds summed over the whole fight, a team may
// wait for energy or cooldowns before its result is flagged.
const waitThreshold = 1.0

// Summarise turns the simulator's JSON output into a Result for job.
func Summarise(job Job, raw []byte) (Result, error) {
	var out struct {
		SimVersion string `json:"sim_version"`
		Statistics struct {
			Iterations int               `json:"iterations"`
			DPS        stat              `json:"dps"`
			Duration   stat              `json:"duration"`
			CharDPS    []stat            `json:"character_dps"`
			ElementDPS map[string]stat   `json:"element_dps"`
			Failed     []map[string]stat `json:"failed_actions"`
		} `json:"statistics"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Result{}, fmt.Errorf("reading simulator output: %w", err)
	}
	s := out.Statistics
	r := Result{
		ID: job.ID, Source: job.Source, Team: job.Team, Weapons: job.Weapons,
		SimVersion: out.SimVersion, Iterations: s.Iterations,
		DPS: s.DPS.Mean, DPSSD: s.DPS.SD, Duration: s.Duration.Mean,
		ElementDPS: map[string]float64{},
	}
	for _, c := range s.CharDPS {
		r.CharDPS = append(r.CharDPS, c.Mean)
	}
	for el, v := range s.ElementDPS {
		if v.Mean > 0 {
			r.ElementDPS[el] = v.Mean
		}
	}
	waited := 0.0
	for _, member := range s.Failed {
		for reason, v := range member {
			if v.Mean > 0 {
				if r.Waits == nil {
					r.Waits = map[string]float64{}
				}
				r.Waits[reason] += v.Mean
				if reason != "swap_cd" { // swapping back too soon is normal play, not a broken rotation
					waited += v.Mean
				}
			}
		}
	}
	if waited > waitThreshold {
		r.Flags = append(r.Flags, fmt.Sprintf("waits %.1fs for energy or cooldowns", waited))
	}
	r.Flags = append(r.Flags, scriptFlags(job)...)
	if job.KeepStats {
		r.Stats = statsLines(raw)
	}
	return r, nil
}

type stat struct {
	Mean float64 `json:"mean"`
	SD   float64 `json:"sd"`
}

// scriptFlags are problems visible in the rotation itself.
func scriptFlags(job Job) []string {
	var flags []string
	for _, c := range job.Team {
		// Iansan's burst buff needs the script to report the active
		// character's movement; without it her buff nearly vanishes.
		if c == "iansan" && !strings.Contains(job.Config, "movement(") {
			flags = append(flags, "iansan without movement: her ATK buff is mostly missing")
		}
	}
	return flags
}

// Progress is reported after every finished job.
type Progress struct {
	Done, Total, Failed int
}

// Run simulates every job not already in cached, on workers goroutines, and
// calls save with each result as it finishes (from a single goroutine, so save
// need not be safe for concurrent use). It stops early if ctx is cancelled.
// It returns how many jobs were skipped because they were cached.
func Run(ctx context.Context, jobs []Job, sim Simulator, workers int, cached map[string]bool,
	save func(Result) error, progress func(Progress)) (skipped int, err error) {

	fp := sim.Fingerprint()
	var todo []Job
	keys := map[string]bool{}
	for _, j := range jobs {
		k := Key(j.Config, fp)
		if cached[k] || keys[k] {
			skipped++
			continue
		}
		keys[k] = true
		todo = append(todo, j)
	}

	queue := make(chan Job)
	results := make(chan Result)
	var wg sync.WaitGroup
	if workers < 1 {
		workers = 1
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range queue {
				results <- simulate(ctx, j, sim, fp)
			}
		}()
	}
	go func() { // feed the queue, then close it so the workers finish
		defer close(queue)
		for _, j := range todo {
			select {
			case queue <- j:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()

	p := Progress{Total: len(todo)}
	for r := range results {
		if r.Error != "" && ctx.Err() != nil {
			continue // cancelled mid-run: do not cache the interruption as a failure
		}
		if err == nil {
			err = save(r)
		}
		p.Done++
		if r.Error != "" {
			p.Failed++
		}
		if progress != nil {
			progress(p)
		}
	}
	if err == nil {
		err = ctx.Err()
	}
	return skipped, err
}

func simulate(ctx context.Context, j Job, sim Simulator, fp string) Result {
	raw, err := sim.Run(ctx, j.Config)
	var r Result
	if err == nil {
		r, err = Summarise(j, raw)
	}
	if err != nil {
		var limit LimitError
		r = Result{ID: j.ID, Source: j.Source, Team: j.Team, Weapons: j.Weapons, Error: err.Error(),
			Final: errors.As(err, &limit)}
	}
	r.Key, r.SimBinary = Key(j.Config, fp), fp
	return r
}

// Teams counts distinct teams (the same four characters in any order) among jobs.
func Teams(jobs []Job) int {
	seen := map[string]bool{}
	for _, j := range jobs {
		t := append([]string(nil), j.Team...)
		sort.Strings(t)
		seen[strings.Join(t, ",")] = true
	}
	return len(seen)
}
