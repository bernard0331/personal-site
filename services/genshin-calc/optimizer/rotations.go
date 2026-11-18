package optimizer

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
	"github.com/bernard0331/personal-site/services/genshin-calc/coarse"
	"github.com/bernard0331/personal-site/services/genshin-calc/good"
	"github.com/bernard0331/personal-site/services/genshin-calc/rotation"
)

// Source is one rotation to consider, before checking the account can field it.
type Source struct {
	ID, From string // rotation id; "kqm-db" or "own"
	Config   string
	Targets  int
	// The database entry's own description and submitter (empty for own
	// files, whose credits are in their comments).
	Description, Submitter string
}

// ReadRotations reads the cached database (one JSON object per line) and the
// own rotation files (*.txt in dir).
func ReadRotations(dbPath, dir string) ([]Source, error) {
	var out []Source
	f, err := os.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var e struct {
			ID          string `json:"id"`
			Config      string `json:"config"`
			Targets     int    `json:"targets"`
			Description string `json:"description"`
			Submitter   string `json:"submitter"`
		}
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("%s: %w", dbPath, err)
		}
		out = append(out, Source{ID: e.ID, From: "kqm-db", Config: e.Config, Targets: e.Targets,
			Description: e.Description, Submitter: e.Submitter})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if dir == "" {
		return out, nil
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.txt"))
	if err != nil {
		return nil, err
	}
	for _, p := range files {
		text, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		id := strings.TrimSuffix(filepath.Base(p), ".txt")
		out = append(out, Source{ID: id, From: "own", Config: string(text), Targets: 1})
	}
	return out, nil
}

type SkipReason struct {
	Reason string
	N      int
}

// MakeJobs keeps the single-target rotations the account can field and writes
// each one's config. It also says how many were left out, and why.
func MakeJobs(sources []Source, acc *good.Account, cat *catalog.Catalog, rule *Rule, iterations int) ([]coarse.Job, []SkipReason) {
	var jobs []coarse.Job
	reasons := map[string]int{}
	playerCons := map[string]int{}
	for _, c := range acc.Characters {
		playerCons[c.Key] = c.Constellation
	}
	for _, s := range sources {
		if s.Targets != 1 {
			reasons["not single-target"]++
			continue
		}
		rot, err := rotation.Parse(s.Config, cat)
		if err != nil {
			reasons["cannot be read: "+err.Error()]++
			continue
		}
		// Database sims built on someone's personal artifacts rate those
		// artifacts, not the team. (Our own rotation files are standard.)
		if s.From == "kqm-db" && !rot.Standard() {
			reasons["built on personal artifact stats"]++
			continue
		}
		cfg, weapons, err := BuildConfig(rot, acc, cat, rule)
		var missing ErrMissingCharacter
		var locked ErrLockedWeapon
		switch {
		case errors.As(err, &missing):
			reasons["account lacks a member"]++
			continue
		case errors.As(err, &locked):
			reasons["account lacks a weapon the script depends on"]++
			continue
		case err != nil:
			reasons[err.Error()]++
			continue
		}
		job := coarse.Job{
			ID: s.ID, Source: s.From, Team: rot.Team(), Weapons: weapons,
			Config: coarse.SetIterations(cfg, iterations),
		}
		for _, m := range rot.Members {
			job.RotCons = append(job.RotCons, m.Cons)
			job.PlayerCons = append(job.PlayerCons, playerCons[m.Key])
		}
		jobs = append(jobs, job)
	}
	var list []SkipReason
	for r, n := range reasons {
		list = append(list, SkipReason{r, n})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].N > list[j].N })
	if len(list) > 8 {
		rest := 0
		for _, s := range list[8:] {
			rest += s.N
		}
		list = append(list[:8], SkipReason{"other reasons", rest})
	}
	return jobs, list
}
