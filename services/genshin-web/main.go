// Command genshin-web is the team optimizer's website: it serves the pages and
// a small JSON API, and runs optimizations in the background with the
// genshin-calc packages.
//
// Routes:
//
//	GET /               the single-page app (web/index.html)
//	GET /static/...     the app's scripts and styles (embedded in the binary)
//	GET /img/<name>.png character, weapon and artifact icons, from -img
//	GET /api/catalog    every released character, weapon and artifact set
//	POST /api/good      read an uploaded GOOD file (untrusted) into an account
//	POST /api/runs      start a run from a locked snapshot; returns its private id
//	GET /api/runs/{id}  a run's progress, and its teams once done
//	GET /healthz        "ok", for the cluster's health checks
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/bernard0331/personal-site/services/genshin-calc/catalog"
)

// web holds the front end. It is embedded so the binary is the whole site,
// apart from the images, which are HoYoverse's and live outside the repo.
//
//go:embed web
var web embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "address to listen on")
	imgDir := flag.String("img", "local/web-img", "directory of icons (tools/fetch_images.py fills it)")
	dataDir := flag.String("data", "local/web-data", "directory for runs and the shared simulation cache")
	cachePath := flag.String("cache", "", "the shared simulation cache (default: cache.jsonl in -data)")
	cacheCopy := flag.String("cache-copy", "", "copy the cache here after every run, and restore it from here at start-up (e.g. on a Cloud Storage mount)")
	dbPath := flag.String("db", "local/rotations/kqm-db.jsonl", "cached KQM Sim Database")
	ownDir := flag.String("own", "services/genshin-calc/rotations", "this project's own rotation files")
	simPath := flag.String("sim", "bin/kqmsim", "simulator binary")
	rankingPath := flag.String("ranking", "local/weapons/ranking.json", "weapon trials' ranking (missing: stand-in weapons)")
	highsPython := flag.String("highs", "local/highs-venv/bin/python", "a Python with highspy (missing: the slower branch and bound)")
	cpus := flag.Int("cpus", max(1, runtime.NumCPU()-2), "cores the simulations may use")
	iterations := flag.Int("iterations", 15, "simulator iterations per rotation in a quick run (15: picks matched 30 within 1-2% on the owner's account)")
	queueSize := flag.Int("queue", 20, "runs that may wait; more are turned away")
	variants := flag.Int("variants", 4, "weapon alternatives per team the solver may choose from")
	perHour := flag.Int("runs-per-hour", 6, "runs one visitor (IP address) may start per hour; 0 for no limit")
	trustProxy := flag.Bool("trust-proxy", false, "take visitors' addresses from X-Forwarded-For (set behind Cloud Run or a load balancer)")
	track := flag.String("track", "", "name of this version during a canary release (stable or canary), sent as the X-Track response header; empty: none")
	proxyHops := flag.Int("proxy-hops", 0, "with -trust-proxy: the visitor is this many X-Forwarded-For entries from the end (2 behind GKE's Gateway); 0 takes the first")
	worker := flag.String("worker", "", "worker mode: simulate one shard of the fan-out batch at this location (gs://bucket/prefix/batch), then exit")
	var fo fanoutConfig
	flag.StringVar(&fo.store, "fanout", os.Getenv("FANOUT_STORE"), "fan big batches of simulations out to workers, through this store (gs://bucket/prefix, or a directory); empty: simulate everything here")
	flag.StringVar(&fo.launch, "fanout-launch", envOr("FANOUT_LAUNCH", "local"), "how workers start: cloudrun:projects/P/locations/R/jobs/J (a Cloud Run Job), or local (child processes, for trying it out)")
	flag.IntVar(&fo.shards, "fanout-shards", envInt("FANOUT_SHARDS", 8), "most workers one batch may use")
	flag.IntVar(&fo.minPerShard, "fanout-min-per-shard", 100, "quick batches: at least this many simulations per worker (smaller batches stay here)")
	flag.IntVar(&fo.workerCPUs, "fanout-worker-cpus", envInt("FANOUT_WORKER_CPUS", 8), "cores each worker has")
	flag.IntVar(&fo.threads, "fanout-threads", 8, "refined batches: cores each team's substat optimiser gets on a worker")
	flag.Parse()
	if *worker != "" {
		if err := runWorker(*worker, *simPath, *cpus); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *track != "" && !trackName.MatchString(*track) {
		log.Fatalf("-track %q: use lower case letters, digits and dashes", *track)
	}

	cat, err := catalog.Load()
	if err != nil {
		log.Fatal(err)
	}
	catalogAPI, err := buildCatalog()
	if err != nil {
		log.Fatal(err)
	}

	eng, err := newEngine(cat, *dataDir, *cachePath, *cacheCopy, *dbPath, *ownDir, *simPath, *rankingPath, *highsPython, *cpus, *iterations, *queueSize)
	if err != nil {
		log.Fatal(err)
	}
	eng.variants = *variants
	if eng.fanout, err = newCoordinator(fo, *simPath); err != nil {
		log.Fatal(err)
	}
	eng.runs.limit = newLimiter(*perHour, *trustProxy)
	eng.runs.limit.proxyHops = *proxyHops
	eng.runs.resume()
	go eng.loop()

	static, _ := fs.Sub(web, "web")
	mux := http.NewServeMux()
	mux.Handle("GET /static/", noCache(http.StripPrefix("/static/", http.FileServerFS(static))))
	mux.Handle("GET /img/", http.StripPrefix("/img/", imageServer(*imgDir)))
	mux.HandleFunc("GET /api/catalog", catalogAPI.serve)
	mux.HandleFunc("POST /api/good", importGOOD(cat))
	mux.HandleFunc("POST /api/runs", eng.runs.create)
	mux.HandleFunc("GET /api/runs/{id}", eng.runs.get)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, static, "index.html")
	})

	srv := &http.Server{
		Addr:              *addr,
		Handler:           logRequests(withTrack(*track, mux)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("listening on http://%s", *addr)
	log.Fatal(srv.ListenAndServe())
}

// envOr and envInt give a flag its default from the environment, so a
// deployment can set it without repeating the image's arguments (the fan-out
// settings name the project, which stays out of the repository).
func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envInt(name string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return n
	}
	return def
}

// imageServer serves PNG icons from dir. http.Dir refuses paths that climb
// out of the directory, and images may be cached for a day.
func imageServer(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		files.ServeHTTP(w, r)
	})
}

// noCache makes browsers check with the server before reusing a file, so a
// new version of the page's scripts is never hidden behind an old copy.
func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

// logRequests writes one line per request: method, path, time taken.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
