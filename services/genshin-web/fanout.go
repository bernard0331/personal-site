package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/bernard0331/personal-site/services/genshin-calc/coarse"
	"github.com/bernard0331/personal-site/services/genshin-calc/fanout"
)

// Fan-out: big batches of simulations go to worker machines (a Cloud Run
// Job's tasks, or a Kubernetes Indexed Job's pods), which run this same
// binary with -worker. See the fanout package.

// fanoutConfig is what the -fanout flags set.
type fanoutConfig struct {
	store       string // gs://bucket/prefix or a directory; empty: no fan-out
	launch      string // cloudrun:projects/P/locations/R/jobs/J, or local
	shards      int    // most workers per batch
	minPerShard int    // quick batches: at least this many simulations per worker
	threads     int    // refined batches: cores per team on a worker
	workerCPUs  int    // cores each worker has
}

// newCoordinator returns nil when fan-out is off.
func newCoordinator(c fanoutConfig, simPath string) (*fanout.Coordinator, error) {
	if c.store == "" {
		return nil, nil
	}
	st, err := fanout.Open(c.store)
	if err != nil {
		return nil, err
	}
	args := []string{"-sim", simPath, "-cpus", fmt.Sprint(c.workerCPUs)}
	var l fanout.Launcher
	switch {
	case strings.HasPrefix(c.launch, "cloudrun:"):
		l = &fanout.CloudRunJob{Name: strings.TrimPrefix(c.launch, "cloudrun:"), Args: args, Token: &fanout.Token{}}
	case c.launch == "local":
		self, err := os.Executable()
		if err != nil {
			return nil, err
		}
		l = &fanout.Processes{Binary: self, Args: args}
	default:
		return nil, fmt.Errorf("-fanout-launch %q: want cloudrun:projects/P/locations/R/jobs/J or local", c.launch)
	}
	log.Printf("fan-out: up to %d workers of %d cores (%s), batches in %s", c.shards, c.workerCPUs, c.launch, c.store)
	return &fanout.Coordinator{Store: st, Launcher: l, Shards: c.shards, MinPerShard: c.minPerShard, Threads: c.threads,
		Timeout: 30 * time.Minute}, nil
}

// worthFanningOut says whether n simulations of kind should go to workers.
// A worker takes 10-30 s to start, about as long as 200 quick simulations
// take here, so small quick batches stay local. A refined team is minutes of
// work, and a worker gives it all its cores, so refined batches always go.
func (e *engine) worthFanningOut(kind string, n int) bool {
	if e.fanout == nil || n == 0 {
		return false
	}
	if kind == fanout.Refine {
		return true
	}
	return n >= 2*max(1, e.fanout.MinPerShard)
}

// runJobs simulates todo with pass p: on workers when fan-out is set up and
// the batch is big enough, then here for whatever the workers didn't return.
// save is called with each result from one goroutine; progress, when set,
// with how many of todo are done.
func (e *engine) runJobs(todo []coarse.Job, p pass, save func(coarse.Result) error, progress func(done, total int)) error {
	ctx := context.Background()
	total := len(todo)
	kind, options := fanout.Quick, ""
	if o, ok := p.sim.(*coarse.Optimiser); ok {
		kind, options = fanout.Refine, o.Options
	}
	done := 0
	if e.worthFanningOut(kind, len(todo)) {
		missing, err := e.fanout.Run(ctx, todo, p.sim, kind, options, func(r coarse.Result) {
			save(r)
			done++
		}, func(n int) {
			if progress != nil {
				progress(n, total)
			}
		})
		if err != nil {
			log.Printf("fan-out: %v; simulating the %d left here", err, len(missing))
		}
		todo = missing
	}
	_, err := coarse.Run(ctx, todo, p.sim, e.workers, nil, save, func(q coarse.Progress) {
		if progress != nil {
			progress(done+q.Done, total)
		}
	})
	return err
}

// runWorker is -worker mode: simulate one shard of the batch at loc and exit.
func runWorker(loc, simPath string, cpus int) error {
	storeLoc, batch, err := fanout.SplitLocation(loc)
	if err != nil {
		return err
	}
	st, err := fanout.Open(storeLoc)
	if err != nil {
		return err
	}
	i, err := fanout.Index()
	if err != nil {
		return err
	}
	return fanout.Work(context.Background(), st, batch, i, cpus, func(m fanout.Manifest) (coarse.Simulator, error) {
		bin, err := coarse.NewBinary(simPath)
		if err != nil {
			return nil, err
		}
		// The same limits as the site's own passes (newEngine); an
		// optimiser with more cores may use more memory.
		bin.Threads = max(1, m.Threads)
		switch m.Kind {
		case fanout.Quick:
			bin.Timeout, bin.MaxMemoryMB = 2*time.Minute, 400
			return bin, nil
		case fanout.Refine:
			bin.Timeout, bin.MaxMemoryMB = 20*time.Minute, 400+100*bin.Threads
			return &coarse.Optimiser{Bin: bin, Options: m.Options}, nil
		}
		return nil, fmt.Errorf("unknown kind %q", m.Kind)
	})
}
