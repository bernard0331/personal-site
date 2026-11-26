// Package fanout spreads a batch of simulations over many machines.
//
// The coordinator (the site) splits the jobs into shards and writes them to
// a Store (a Cloud Storage folder) next to a manifest. A Launcher then starts
// one worker per shard: a Cloud Run Job's tasks or a Kubernetes Indexed Job's
// pods, all running the same image as the site. Each worker reads its shard,
// simulates it with the coarse package's worker pool, and writes its results
// back, now and then while it works and once more at the end, followed by a
// done marker. The coordinator collects results as they appear.
//
// Workers run the same simulator build as the coordinator, which the
// manifest's fingerprint checks, so their results go into the shared cache
// under the same keys. Whatever a batch doesn't return in time (a worker
// failed, the launch was refused) is handed back to the caller to simulate
// itself.
//
// Layout of a batch under the store's prefix:
//
//	<batch>/manifest.json   what kind of pass, the fingerprint, shards, threads
//	<batch>/shard-007.jsonl  shard 7's jobs, one coarse.Job per line
//	<batch>/results-007.jsonl its results so far, one coarse.Result per line
//	<batch>/done-007.json    written last: shard 7 is finished
package fanout

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/bernard0331/personal-site/services/genshin-calc/coarse"
)

// Kinds of pass a batch can be.
const (
	Quick  = "quick"  // the simulator alone
	Refine = "refine" // KQM Sim's substat optimiser, then the simulator
)

// Manifest describes a batch to its workers.
type Manifest struct {
	Batch       string    `json:"batch"`
	Kind        string    `json:"kind"`
	Options     string    `json:"options,omitempty"` // the optimiser's -options, for Refine
	Fingerprint string    `json:"fingerprint"`       // the coordinator's simulator, which workers must match
	Shards      int       `json:"shards"`
	Threads     int       `json:"threads"` // cores per simulation
	Created     time.Time `json:"created"`
}

// Done is a worker's done marker.
type Done struct {
	Shard   int     `json:"shard"`
	Results int     `json:"results"`
	Seconds float64 `json:"seconds"`
	Error   string  `json:"error,omitempty"`
}

// ErrNotFound is what Store.Get returns for a missing object.
var ErrNotFound = errors.New("not found")

// Store holds a batch's files. Names are relative to the store's own prefix.
type Store interface {
	Put(ctx context.Context, name string, data []byte) error
	Get(ctx context.Context, name string) ([]byte, error)
	List(ctx context.Context, prefix string) ([]string, error)
	// Location is where a worker finds the store: a gs:// URL or a directory.
	Location() string
}

// Launcher starts shards workers for the batch at location (the store's
// Location joined with the batch id). It returns once they are started, not
// finished.
type Launcher interface {
	Launch(ctx context.Context, location string, shards int) error
}

// Coordinator runs batches. Shards is the most workers one batch may use;
// MinPerShard keeps quick batches from being cut so thin that start-up costs
// more than the simulating.
type Coordinator struct {
	Store           Store
	Launcher        Launcher
	Shards          int
	MinPerShard     int           // quick batches: at least this many jobs per worker (default 100)
	MaxRefineShards int           // refined batches: most shards, one team each (default 256)
	Threads         int           // refine batches: cores per optimiser (default 1)
	Poll            time.Duration // how often to look for results (default 3 s)
	Timeout         time.Duration // how long to wait for the whole batch (default 15 min)
}

// Plan returns how many shards a batch of n jobs of kind gets.
//
// A refined batch gets one shard per team (up to MaxRefineShards, default
// 256): teams take 25-80 s each, so fixed shards of two or three left some
// workers busy four times as long as others (2026-11-26). With one team per
// request, a worker that finishes takes the next waiting team; the Service
// launcher keeps asking while every instance is busy.
func (c *Coordinator) Plan(kind string, n int) int {
	s := max(1, c.Shards)
	if kind == Refine {
		s = c.MaxRefineShards
		if s <= 0 {
			s = 256
		}
	}
	if kind == Quick {
		per := c.MinPerShard
		if per <= 0 {
			per = 100
		}
		s = min(s, (n+per-1)/per)
	}
	return max(1, min(s, n))
}

// Run simulates jobs on workers. save is called with each result as it
// arrives (from one goroutine); progress, when set, with how many are done.
// Jobs that came back from no worker are returned for the caller to run.
// The error says why, when the batch did not finish.
func (c *Coordinator) Run(ctx context.Context, jobs []coarse.Job, sim coarse.Simulator, kind, options string,
	save func(coarse.Result), progress func(done int)) ([]coarse.Job, error) {

	if len(jobs) == 0 {
		return nil, nil
	}
	fp := sim.Fingerprint()
	shards := c.Plan(kind, len(jobs))
	threads := 1
	if kind == Refine && c.Threads > 0 {
		threads = c.Threads
	}
	m := Manifest{Batch: newID(), Kind: kind, Options: options, Fingerprint: fp, Shards: shards, Threads: threads, Created: time.Now().UTC()}

	// Round-robin, so slow jobs (which tend to sit together) are spread out.
	parts := make([][]coarse.Job, shards)
	want := map[string]coarse.Job{}
	for i, j := range jobs {
		parts[i%shards] = append(parts[i%shards], j)
		want[coarse.Key(j.Config, fp)] = j
	}
	// All shards at once: one after another took 7 s for 16 from a laptop.
	errs := make([]error, shards)
	var wg sync.WaitGroup
	for i, p := range parts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = c.Store.Put(ctx, shardName(m.Batch, "shard", i, "jsonl"), jsonLines(p))
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			return jobs, fmt.Errorf("writing shard %d: %w", i, err)
		}
	}
	mb, _ := json.Marshal(m)
	if err := c.Store.Put(ctx, m.Batch+"/manifest.json", mb); err != nil {
		return jobs, fmt.Errorf("writing manifest: %w", err)
	}
	start := time.Now()
	if err := c.Launcher.Launch(ctx, joinLocation(c.Store.Location(), m.Batch), shards); err != nil {
		return jobs, fmt.Errorf("launching %d workers: %w", shards, err)
	}
	log.Printf("fanout %s: %d %s jobs on %d workers", m.Batch, len(jobs), kind, shards)

	poll, timeout := c.Poll, c.Timeout
	if poll <= 0 {
		poll = 3 * time.Second
	}
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	got := map[string]bool{}
	finished := map[int]bool{}
	collect := func(i int) {
		b, err := c.Store.Get(ctx, shardName(m.Batch, "results", i, "jsonl"))
		if err != nil {
			return
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			var r coarse.Result
			if json.Unmarshal(sc.Bytes(), &r) != nil || got[r.Key] {
				continue
			}
			j, ok := want[r.Key]
			if !ok {
				continue
			}
			got[r.Key] = true
			r.ID, r.Source, r.Team, r.Weapons, r.RotCons, r.PlayerCons = j.ID, j.Source, j.Team, j.Weapons, j.RotCons, j.PlayerCons
			save(r)
		}
		if progress != nil {
			progress(len(got))
		}
	}
	var err error
	for tick := 0; len(finished) < shards; tick++ {
		select {
		case <-ctx.Done():
			err = fmt.Errorf("%d of %d workers finished in time: %w", len(finished), shards, ctx.Err())
		case <-time.After(poll):
		}
		if err != nil {
			break
		}
		names, lerr := c.Store.List(ctx, m.Batch+"/done-")
		if lerr != nil {
			continue
		}
		for _, n := range names {
			var i int
			if _, e := fmt.Sscanf(n[strings.LastIndex(n, "done-"):], "done-%03d.json", &i); e != nil || finished[i] {
				continue
			}
			finished[i] = true
			collect(i)
		}
		if tick%3 == 2 { // partial results now and then, for the progress bar
			for i := range shards {
				if !finished[i] {
					collect(i)
				}
			}
		}
	}
	log.Printf("fanout %s: %d of %d results in %s", m.Batch, len(got), len(jobs), time.Since(start).Round(time.Second))

	var missing []coarse.Job
	for k, j := range want {
		if !got[k] {
			missing = append(missing, j)
		}
	}
	return missing, err
}

// SimulatorFor builds the simulator a manifest asks for.
type SimulatorFor func(m Manifest) (coarse.Simulator, error)

// Work runs shard index of the batch at the store's prefix: the worker side.
// cpus is how many cores the worker has.
func Work(ctx context.Context, store Store, batch string, index, cpus int, simFor SimulatorFor) error {
	start := time.Now()
	mb, err := store.Get(ctx, batch+"/manifest.json")
	if err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(mb, &m); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	done := func(results int, werr error) error {
		d := Done{Shard: index, Results: results, Seconds: time.Since(start).Seconds()}
		if werr != nil {
			d.Error = werr.Error()
		}
		b, _ := json.Marshal(d)
		if err := store.Put(ctx, shardName(batch, "done", index, "json"), b); err != nil {
			return err
		}
		return werr
	}
	if index < 0 || index >= m.Shards {
		return fmt.Errorf("shard %d of %d", index, m.Shards)
	}
	sim, err := simFor(m)
	if err != nil {
		return done(0, err)
	}
	if fp := sim.Fingerprint(); fp != m.Fingerprint {
		return done(0, fmt.Errorf("simulator %s, but the batch was made with %s", fp, m.Fingerprint))
	}
	sb, err := store.Get(ctx, shardName(batch, "shard", index, "jsonl"))
	if err != nil {
		return done(0, fmt.Errorf("shard: %w", err))
	}
	var jobs []coarse.Job
	sc := bufio.NewScanner(bytes.NewReader(sb))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var j coarse.Job
		if err := json.Unmarshal(sc.Bytes(), &j); err != nil {
			return done(0, fmt.Errorf("shard: %w", err))
		}
		jobs = append(jobs, j)
	}

	var mu sync.Mutex
	var out bytes.Buffer
	n := 0
	flush := func() error {
		mu.Lock()
		b := append([]byte(nil), out.Bytes()...)
		mu.Unlock()
		return store.Put(ctx, shardName(batch, "results", index, "jsonl"), b)
	}
	stop := make(chan struct{})
	flushed := make(chan struct{})
	go func() { // partial results every 10 s, for the coordinator's progress
		defer close(flushed)
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if err := flush(); err != nil {
					log.Printf("shard %d: writing partial results: %v", index, err)
				}
			}
		}
	}()
	workers := max(1, cpus/max(1, m.Threads))
	_, rerr := coarse.Run(ctx, jobs, sim, workers, nil, func(r coarse.Result) error {
		b, err := json.Marshal(r)
		if err != nil {
			return err
		}
		mu.Lock()
		out.Write(b)
		out.WriteByte('\n')
		n++
		mu.Unlock()
		return nil
	}, nil)
	close(stop)
	<-flushed
	if err := flush(); err != nil {
		return fmt.Errorf("results: %w", err)
	}
	log.Printf("shard %d of %s: %d results in %s (%d at once)", index, batch, n, time.Since(start).Round(time.Second), workers)
	return done(n, rerr)
}

func shardName(batch, kind string, i int, ext string) string {
	return fmt.Sprintf("%s/%s-%03d.%s", batch, kind, i, ext)
}

func jsonLines[T any](xs []T) []byte {
	var b bytes.Buffer
	for _, x := range xs {
		line, _ := json.Marshal(x)
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

func joinLocation(loc, batch string) string {
	return strings.TrimSuffix(loc, "/") + "/" + batch
}

// SplitLocation splits a batch's location (what a worker is given) into the
// store's location and the batch id.
func SplitLocation(loc string) (store, batch string, err error) {
	loc = strings.TrimSuffix(loc, "/")
	i := strings.LastIndex(loc, "/")
	if i <= 0 || i == len(loc)-1 {
		return "", "", fmt.Errorf("batch location %q: want <store>/<batch>", loc)
	}
	return loc[:i], loc[i+1:], nil
}

func newID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}
