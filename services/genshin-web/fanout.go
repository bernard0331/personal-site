package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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
	case strings.HasPrefix(c.launch, "service:"):
		l = &fanout.Service{URL: strings.TrimPrefix(c.launch, "service:"), Token: &fanout.IDToken{}, Log: log.Printf}
	case strings.HasPrefix(c.launch, "k8s:"):
		// k8s:NAMESPACE: an Indexed Job on the worker node pool (GKE).
		l = &fanout.IndexedJob{Namespace: strings.TrimPrefix(c.launch, "k8s:"), ServiceAccount: "genshin-web", Args: args,
			NodeSelector: map[string]string{"personal-site/pool": "workers"}, Toleration: "personal-site/workers",
			CPU: fmt.Sprintf("%dm", c.workerCPUs*1000-500), Memory: "3Gi", Container: "genshin-web"}
	case c.launch == "local":
		self, err := os.Executable()
		if err != nil {
			return nil, err
		}
		l = &fanout.Processes{Binary: self, Args: args}
	default:
		return nil, fmt.Errorf("-fanout-launch %q: want service:https://URL, cloudrun:projects/P/locations/R/jobs/J, k8s:NAMESPACE or local", c.launch)
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
// The shard number comes from the platform (fanout.Index).
func runWorker(loc, simPath string, cpus int) error {
	i, err := fanout.Index()
	if err != nil {
		return err
	}
	return workShard(context.Background(), loc, i, simPath, cpus)
}

// serveWorker is -worker-serve mode: a Cloud Run service that runs one shard
// per request (POST /shard, a fanout.ShardRequest), answering when it's done.
// The service allows one request per instance, so each shard gets a whole
// instance, and Cloud Run starts as many instances as there are shards.
func serveWorker(addr, simPath string, cpus int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("POST /shard", func(w http.ResponseWriter, r *http.Request) {
		var req fanout.ShardRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
			httpError(w, http.StatusBadRequest, "not a shard request")
			return
		}
		start := time.Now()
		if err := workShard(r.Context(), req.Location, req.Index, simPath, cpus); err != nil {
			log.Printf("shard %d of %s: %v", req.Index, req.Location, err)
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		fmt.Fprintf(w, "shard %d done in %s\n", req.Index, time.Since(start).Round(time.Second))
	})
	log.Printf("worker service on %s, %d cores", addr, cpus)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	return srv.ListenAndServe()
}

// workShard simulates shard i of the batch at loc.
func workShard(ctx context.Context, loc string, i int, simPath string, cpus int) error {
	storeLoc, batch, err := fanout.SplitLocation(loc)
	if err != nil {
		return err
	}
	st, err := fanout.Open(storeLoc)
	if err != nil {
		return err
	}
	return fanout.Work(ctx, st, batch, i, cpus, func(m fanout.Manifest) (coarse.Simulator, error) {
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
