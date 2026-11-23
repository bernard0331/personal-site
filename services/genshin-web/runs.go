package main

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
)

// A run is one locked request to optimize: the account and options as they
// were when the visitor pressed Optimize. Runs live in their own directory
// under -data: snapshot.json (what was asked), status.json (progress) and
// result.json (the teams). The id is random and unguessable, so it doubles as
// the run's private link.

// Snapshot is what the page sends. Everything in it is checked by validate.
type Snapshot struct {
	Characters map[string]int   `json:"characters"` // key -> constellation
	Weapons    map[string][]int `json:"weapons"`    // key -> refinement of each copy
	Unlimited  bool             `json:"unlimited"`  // 3-stars and craftable 4-stars: unlimited R5
	Energy     int              `json:"energy"`     // 1: KQM standard drops, 2: twice as often
	Pins       []string         `json:"pins"`       // characters every answer must include
	Excluded   []string         `json:"excluded"`   // characters never used
	// Mode is "quick" (or empty) for a quick run, or a refined run's level:
	// "light", "standard" or "thorough" (see refineLevels).
	Mode      string         `json:"mode,omitempty"`
	Artifacts string         `json:"artifacts,omitempty"` // refined runs: "kqm" (or empty) or "top1"
	Refine    *RefineOptions `json:"refine,omitempty"`    // thorough runs: the advanced settings
}

// RefineOptions say which teams a refined run simulates again, and how
// carefully. Light and standard runs use fixed settings; a thorough run
// starts from its own and lets the visitor change them.
type RefineOptions struct {
	Top          int  `json:"top"`           // best teams overall
	PerElement   int  `json:"per_element"`   // best teams of each main element
	PerCharacter int  `json:"per_character"` // best teams each character is in
	Iterations   int  `json:"iterations"`    // simulator iterations per refined team
	SkipWaiting  bool `json:"skip_waiting"`  // leave out teams that wait for energy or cooldowns
}

// refined reports whether the snapshot asks for a refined run.
func (s *Snapshot) refined() bool { return s.Mode != "" && s.Mode != "quick" }

// Limits on one snapshot, far above any real account, so a hostile request
// cannot make a run do unbounded work.
const (
	maxSnapshotBytes = 1 << 20
	maxCopies        = 50 // copies of one weapon
	maxWeaponCopies  = 3000
)

func (s *Snapshot) validate(cat *catalog.Catalog) error {
	if len(s.Characters) == 0 {
		return errors.New("add at least one character")
	}
	for k, c := range s.Characters {
		ch, ok := cat.Character(k)
		if !ok || !ch.Released || strings.HasPrefix(k, "aether") {
			return fmt.Errorf("unknown character %q", k)
		}
		if c < 0 || c > 6 {
			return fmt.Errorf("%s: constellation %d is out of range", k, c)
		}
	}
	total := 0
	for k, rs := range s.Weapons {
		if _, ok := cat.Weapon(k); !ok {
			return fmt.Errorf("unknown weapon %q", k)
		}
		if len(rs) > maxCopies {
			return fmt.Errorf("%s: more than %d copies", k, maxCopies)
		}
		for _, r := range rs {
			if r < 1 || r > 5 {
				return fmt.Errorf("%s: refinement %d is out of range", k, r)
			}
		}
		total += len(rs)
	}
	if total > maxWeaponCopies {
		return fmt.Errorf("more than %d weapon copies", maxWeaponCopies)
	}
	if s.Energy != 1 && s.Energy != 2 {
		return errors.New("energy must be 1 or 2")
	}
	for _, list := range [][]string{s.Pins, s.Excluded} {
		if len(list) > 10 {
			return errors.New("at most 10 pinned or left-out characters")
		}
		for _, k := range list {
			if _, ok := s.Characters[k]; !ok {
				return fmt.Errorf("%q is not in the account", k)
			}
		}
	}
	switch s.Mode {
	case "", "quick", "light", "standard", "thorough":
	default:
		return fmt.Errorf("unknown run %q", s.Mode)
	}
	switch s.Artifacts {
	case "", "kqm":
	case "top1":
		if !s.refined() {
			return errors.New("top 1% artifacts need a refined run")
		}
	default:
		return fmt.Errorf("unknown artifacts %q", s.Artifacts)
	}
	if s.Refine != nil {
		r := s.Refine
		switch {
		case s.Mode != "thorough":
			return errors.New("refine settings are for thorough runs")
		case r.Top < 0 || r.Top > 500, r.PerElement < 0 || r.PerElement > 50, r.PerCharacter < 0 || r.PerCharacter > 5:
			return errors.New("refine settings are out of range")
		case r.Iterations != 250 && r.Iterations != 500 && r.Iterations != 1000 && r.Iterations != 2000:
			return errors.New("iterations must be 250, 500, 1000 or 2000")
		}
	}
	for _, p := range s.Pins {
		for _, e := range s.Excluded {
			if p == e {
				return fmt.Errorf("%s is both pinned and left out", p)
			}
		}
	}
	return nil
}

// Status is a run's progress, as GET /api/runs/{id} reports it.
type Status struct {
	State     string    `json:"state"` // queued, running, solving, refining, done or failed
	Done      int       `json:"done"`  // simulations finished (including cached)
	Total     int       `json:"total"`
	Cached    int       `json:"cached"` // of Done, how many came from the shared cache
	Error     string    `json:"error,omitempty"`
	Created   time.Time `json:"created"`
	Finished  time.Time `json:"finished,omitzero"`
	QueueSize int       `json:"queue,omitempty"` // runs ahead of this one, while queued
}

var runID = regexp.MustCompile(`^[a-z2-7]{26}$`)

func newRunID() string {
	b := make([]byte, 16)
	rand.Read(b) // crypto/rand: unguessable
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

// store keeps runs on disk. Writes go through a temporary file and a rename,
// so a reader never sees half a file.
type store struct{ dir string }

func (s store) path(id, name string) string { return filepath.Join(s.dir, "runs", id, name) }

func (s store) write(id, name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	p := s.path(id, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (s store) read(id, name string, v any) error {
	b, err := os.ReadFile(s.path(id, name))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// runs is the runs API: create, look up, and the queue in between.
type runs struct {
	cat    *catalog.Catalog
	store  store
	queue  chan string
	mu     sync.Mutex
	status map[string]*Status // runs this process has seen; finished ones are also on disk
	order  []string           // queued ids, oldest first
	limit  *limiter           // per-visitor limits; nil for none
}

func newRuns(cat *catalog.Catalog, dir string, queueSize int) *runs {
	return &runs{cat: cat, store: store{dir}, queue: make(chan string, queueSize), status: map[string]*Status{}}
}

// create handles POST /api/runs: check the snapshot, save it, queue it.
func (rs *runs) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSnapshotBytes)
	var snap Snapshot
	if err := json.NewDecoder(r.Body).Decode(&snap); err != nil {
		httpError(w, http.StatusBadRequest, "the request is not a valid snapshot")
		return
	}
	if err := snap.validate(rs.cat); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	var ip string
	if rs.limit != nil {
		ip = rs.limit.clientIP(r)
		if why := rs.limit.allow(ip, rs.finished); why != "" {
			httpError(w, http.StatusTooManyRequests, why)
			return
		}
	}
	id := newRunID()
	st := &Status{State: "queued", Created: time.Now().UTC()}
	if err := rs.store.write(id, "snapshot.json", snap); err != nil {
		log.Printf("run %s: %v", id, err)
		httpError(w, http.StatusInternalServerError, "the run couldn't be saved")
		return
	}
	rs.setStatus(id, st)
	select {
	case rs.queue <- id:
		rs.mu.Lock()
		rs.order = append(rs.order, id)
		rs.mu.Unlock()
		if rs.limit != nil {
			rs.limit.record(ip, id)
		}
	default:
		rs.setStatus(id, &Status{State: "failed", Error: "the optimizer is busy; try again in a few minutes", Created: st.Created})
		httpError(w, http.StatusServiceUnavailable, "the optimizer is busy; try again in a few minutes")
		return
	}
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]string{"id": id})
}

// get handles GET /api/runs/{id}: the status, and the result once done.
func (rs *runs) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !runID.MatchString(id) {
		httpError(w, http.StatusNotFound, "no such run")
		return
	}
	st, ok := rs.statusOf(id)
	if !ok {
		httpError(w, http.StatusNotFound, "no such run")
		return
	}
	out := map[string]any{"status": st}
	var snap Snapshot
	if rs.store.read(id, "snapshot.json", &snap) == nil {
		out["snapshot"] = snap
	}
	// A refined run saves its quick result first, so the page can show it
	// while the refined one is computed.
	var res json.RawMessage
	if err := rs.store.read(id, "result.json", &res); err == nil {
		out["result"] = res
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, out)
}

func (rs *runs) setStatus(id string, st *Status) {
	rs.mu.Lock()
	rs.status[id] = st
	rs.mu.Unlock()
	if err := rs.store.write(id, "status.json", st); err != nil {
		log.Printf("run %s: saving status: %v", id, err)
	}
}

func (rs *runs) statusOf(id string) (Status, bool) {
	rs.mu.Lock()
	st, ok := rs.status[id]
	if ok {
		out := *st
		if out.State == "queued" {
			for i, q := range rs.order {
				if q == id {
					out.QueueSize = i
				}
			}
		}
		rs.mu.Unlock()
		return out, true
	}
	rs.mu.Unlock()
	var disk Status
	if rs.store.read(id, "status.json", &disk) != nil {
		return Status{}, false
	}
	if disk.State != "done" && disk.State != "failed" {
		// Left unfinished when the server restarted, and not resumed (resume
		// takes only recent runs, as many as the queue holds).
		disk.State, disk.Error = "failed", "the server restarted during this run; start it again"
	}
	return disk, true
}

// resumeWindow is how old an unfinished run may be and still be resumed.
const resumeWindow = 48 * time.Hour

// resume queues again the runs a previous server left unfinished (Cloud Run
// stops an instance some minutes after the last visit, even mid-run), oldest
// first. Simulations done before the stop are in the shared cache, so a
// resumed run picks up close to where it was.
func (rs *runs) resume() {
	dirs, err := os.ReadDir(filepath.Join(rs.store.dir, "runs"))
	if err != nil {
		return
	}
	type pending struct {
		id string
		st Status
	}
	var list []pending
	for _, d := range dirs {
		var st Status
		if !d.IsDir() || !runID.MatchString(d.Name()) || rs.store.read(d.Name(), "status.json", &st) != nil {
			continue
		}
		if st.State != "done" && st.State != "failed" && time.Since(st.Created) < resumeWindow {
			list = append(list, pending{d.Name(), st})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].st.Created.Before(list[j].st.Created) })
	for _, p := range list {
		st := &Status{State: "queued", Created: p.st.Created}
		select {
		case rs.queue <- p.id:
			rs.setStatus(p.id, st)
			rs.mu.Lock()
			rs.order = append(rs.order, p.id)
			rs.mu.Unlock()
			log.Printf("run %s: resumed after a restart", p.id)
		default:
			return
		}
	}
}

// finished reports whether a run is over (done or failed, or unknown).
func (rs *runs) finished(id string) bool {
	st, ok := rs.statusOf(id)
	return !ok || st.State == "done" || st.State == "failed"
}

// next takes the oldest queued run off the queue (blocking).
func (rs *runs) next() string {
	id := <-rs.queue
	rs.mu.Lock()
	if len(rs.order) > 0 && rs.order[0] == id {
		rs.order = rs.order[1:]
	}
	rs.mu.Unlock()
	return id
}
