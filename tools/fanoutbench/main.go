// Command fanoutbench times the fan-out with a fixed batch of real rotation
// configs, without touching the site's cache: every simulation is from
// scratch, the case the fan-out exists for.
//
//	go run ./tools/fanoutbench -n 4230 -shards 8 \
//	  -store gs://BUCKET/fanout -launch cloudrun:projects/P/locations/R/jobs/genshin-sim \
//	  -fingerprint SIM_BINARY
//
// -fingerprint is the workers' simulator build (a result's sim_binary in
// the site's cache), since this machine's simulator build may differ. With
// -launch local, workers are child processes of -worker-binary and the
// fingerprint is taken from -sim.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bernard0331/personal-site/services/genshin-calc/coarse"
	"github.com/bernard0331/personal-site/services/genshin-calc/fanout"
)

// claim is a simulator that is never run here: it only names the workers'
// build, so the batch's keys match theirs.
type claim string

func (c claim) Fingerprint() string                         { return string(c) }
func (c claim) Run(context.Context, string) ([]byte, error) { return nil, fmt.Errorf("not run here") }

func main() {
	db := flag.String("db", "local/rotations/kqm-db.jsonl", "cached KQM Sim Database")
	n := flag.Int("n", 4230, "simulations in the batch")
	iterations := flag.Int("iterations", 15, "iterations per simulation")
	kind := flag.String("kind", fanout.Quick, "quick, refine or measure (the seeded simulator)")
	seedFlag := flag.Int64("seed", 1, "measure: iteration i runs with seed SEED+i")
	dump := flag.String("dump", "", "write each result's id and DPS here (JSON lines)")
	options := flag.String("options", "", "refine: the optimiser's -options")
	seed := flag.Int64("seed", 1, "which rotations (the same seed gives the same batch)")
	store := flag.String("store", "", "gs://bucket/prefix or a directory")
	launch := flag.String("launch", "local", "service:https://URL, cloudrun:projects/P/locations/R/jobs/J or local")
	shards := flag.Int("shards", 8, "workers")
	minPer := flag.Int("min-per-shard", 1, "quick: at least this many simulations per worker")
	threads := flag.Int("threads", 8, "refine: cores per team on a worker")
	refineShards := flag.Int("refine-shards", 64, "refine: most shards (one team each until there are more teams)")
	workerCPUs := flag.Int("worker-cpus", 8, "cores each worker has")
	fp := flag.String("fingerprint", "", "the workers' simulator build (required unless -launch local)")
	sim := flag.String("sim", "bin/kqmsim", "local: the simulator the workers run")
	workerBin := flag.String("worker-binary", "bin/genshin-web", "local: the genshin-web binary to run as workers")
	flag.Parse()

	jobs, err := batch(*db, *n, *iterations, *seed)
	if err != nil {
		log.Fatal(err)
	}
	st, err := fanout.Open(*store)
	if err != nil || *store == "" {
		log.Fatalf("-store: %v", err)
	}
	args := []string{"-sim", *sim, "-cpus", fmt.Sprint(*workerCPUs)}
	var l fanout.Launcher
	var s coarse.Simulator
	switch {
	case strings.HasPrefix(*launch, "cloudrun:"):
		if *fp == "" {
			log.Fatal("-fingerprint is required with cloudrun")
		}
		args[1] = "/app/kqmsim"
		l = &fanout.CloudRunJob{Name: strings.TrimPrefix(*launch, "cloudrun:"), Args: args, Token: &fanout.Token{}}
		s = claim(*fp)
	case strings.HasPrefix(*launch, "service:"):
		if *fp == "" {
			log.Fatal("-fingerprint is required with service")
		}
		l = &fanout.Service{URL: strings.TrimPrefix(*launch, "service:"), Token: &fanout.IDToken{}, Log: log.Printf, Parallel: *shards}
		s = claim(*fp)
	case *launch == "local":
		l = &fanout.Processes{Binary: *workerBin, Args: args}
		b, err := coarse.NewBinary(*sim)
		if err != nil {
			log.Fatal(err)
		}
		s = b
	default:
		log.Fatalf("-launch %q", *launch)
	}
	if *kind == fanout.Measure {
		s = fanout.Label("bench-measure")
	}
	if *kind == fanout.Refine {
		switch v := s.(type) {
		case *coarse.Binary:
			s = &coarse.Optimiser{Bin: v, Options: *options}
		case claim: // the same suffix coarse.Optimiser adds
			if *options == "" {
				s = claim(string(v) + "+substats")
			} else {
				s = claim(string(v) + "+substats:" + *options)
			}
		}
	}
	c := &fanout.Coordinator{Store: st, Launcher: l, Shards: *shards, MinPerShard: *minPer, MaxRefineShards: *refineShards, Threads: *threads, Seed: *seedFlag, Timeout: time.Hour}
	start := time.Now()
	var mu sync.Mutex
	var first time.Duration
	var out *os.File
	if *dump != "" {
		if out, err = os.Create(*dump); err != nil {
			log.Fatal(err)
		}
		defer out.Close()
	}
	failed := 0
	got := 0
	missing, err := c.Run(context.Background(), jobs, s, *kind, *options, func(r coarse.Result) {
		mu.Lock()
		defer mu.Unlock()
		if got == 0 {
			first = time.Since(start)
		}
		got++
		if r.Error != "" {
			failed++
		}
		if out != nil {
			b, _ := json.Marshal(map[string]any{"id": r.ID, "dps": r.DPS, "sd": r.DPSSD, "error": r.Error})
			out.Write(append(b, '\n'))
		}
	}, nil)
	wall := time.Since(start)
	if err != nil {
		log.Printf("batch: %v", err)
	}
	fmt.Printf("%d %s simulations on %d workers: %s (first results after %s); %d results, %d failed, %d missing\n",
		len(jobs), *kind, c.Plan(*kind, len(jobs)), wall.Round(time.Second), first.Round(time.Second), got, failed, len(missing))
}

// batch picks n rotations with four members from the database, at the given
// iteration count.
func batch(path string, n, iterations int, seed int64) ([]coarse.Job, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var all []coarse.Job
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var row struct {
			ID     string   `json:"id"`
			Config string   `json:"config"`
			Chars  []string `json:"chars"`
		}
		if json.Unmarshal(sc.Bytes(), &row) != nil || len(row.Chars) != 4 {
			continue
		}
		all = append(all, coarse.Job{ID: row.ID, Source: "kqm-db", Team: row.Chars, Config: coarse.SetIterations(row.Config, iterations)})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	rand.New(rand.NewSource(seed)).Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
	if n < len(all) {
		all = all[:n]
	}
	return all, nil
}
