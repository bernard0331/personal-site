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
}

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
	State     string    `json:"state"` // queued, running, solving, done or failed
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
	if st.State == "done" {
		var res json.RawMessage
		if err := rs.store.read(id, "result.json", &res); err == nil {
			out["result"] = res
		}
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
		// Left unfinished when the server restarted; it will not resume.
		disk.State, disk.Error = "failed", "the server restarted during this run; start it again"
	}
	return disk, true
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
