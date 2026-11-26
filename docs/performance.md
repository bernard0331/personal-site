# Performance notes

Where a run's time goes, and what was tried to make it faster. Measured on
the owner's laptop (Intel i5-8257U, 4 cores, 8 threads) and from Cloud Run's
logs, 2026-11-25.

## Where the time goes

**Quick run** (4,230 simulations at 15 iterations on the owner's account):

| | Time |
| :- | :- |
| From scratch, Cloud Run (8 vCPU) | 5 min 32 s to 7 min 43 s |
| From scratch, laptop (8 threads) | 5 min 46 s |
| Everything cached, Cloud Run | 35 to 39 s (the solve and its confirmation rounds) |

So about 90% of a run from scratch is simulating. Starting a simulator
process costs about 0.06 s, 20-28% of a 15-iteration simulation (each
iteration takes 8-18 ms on one core); the system time for spawning
processes was another 9%. Simulating in one long-lived process would save at
most about a quarter.

**Refined run** (per team, one core):

| Step | Time | Share |
| :- | :- | :- |
| KQM Sim's substat optimiser | 44-125 s | about 85% |
| Final simulation, 1,000 iterations | 11-23 s | about 15% |

A light run on Cloud Run (18 teams) took 2 min 59 s.

## What was tried

| Change | Result | Kept? |
| :- | :- | :- |
| Final simulation at 250 iterations (500 for thorough) instead of 1,000 | 11 teams' final simulations: 42.9 s → 11.4 s (500: 19.1 s); about 11% off a light or standard run, 8% off thorough. Standard error of DPS rises from a median 0.08% to 0.15% (0.11% at 500), below the optimiser's own run-to-run variation | yes |
| Optimiser without its fine-tune step (`fine_tune=0`) | 44% less optimiser time, but DPS fell on 14 of 16 teams: median -0.64%, and -8%, -10% and -32% on three. Two default runs of the same team differ by at most 0.9% | no |
| Each optimiser on all 8 threads instead of one | about 10% slower (8 and 11 teams), 4x the memory per process | no |
| 4 optimisers on 2 threads each instead of 8 on one | the same wall time (239 s for 11 teams) | no |

The optimiser itself scales well (one team: 44.3 s on one core, 10.7 s on
four), but on a busy machine one core per team already keeps every core
working. A run's wall time is set by its slowest teams; spreading teams over
more machines is what shortens it.

The optimiser's internal simulations don't follow the config's iteration
count, and it rejects a `sim_iter` option, so there is nothing to tune there.

## Fan-out to worker machines (2026-11-26)

Simulations that aren't cached go to workers on other machines
([services/genshin-calc/fanout](../services/genshin-calc/fanout)): the site
splits them into shards in the bucket, each worker (the site's own image,
8 vCPU) simulates one, and the site collects the results. Quick batches of
200 or more go out in shards of at least 100; every refined team is its own
shard. Timed with [tools/fanoutbench](../tools/fanoutbench), 4,230 quick
simulations from scratch at 15 iterations:

| Workers | How they start | Total | Of which |
| :- | :- | :- | :- |
| 1 (the site alone, before) | — | 5 min 32 s to 7 min 43 s | |
| 8 | Cloud Run Job, one task per shard | 43 s to 2 min 14 s | tasks waited 17-105 s to start, then simulated in 10-15 s |
| 16 | Cloud Run Job | 1 min 14 s to 2 min 6 s | tasks waited 53-100 s |
| 8 | Cloud Run service, one request per instance | 41-45 s | |
| 16 | Cloud Run service | 34-41 s | about 20 s of it is instances starting and simulating; the rest uploading shards and noticing results |

A Job's tasks all start together, after a delay that varies run to run and
dwarfs the work, so the site uses a private worker service (concurrency 1,
scales to zero) and keeps the Job as an option (`FANOUT_VIA=job`). A burst of
requests can outrun instance start-up; Cloud Run then answers 429 or 500 ("no
available instance"), and the launcher asks again every 1-2 s.

Through the live site, an uncached quick run on the owner's account took
about 1.5 minutes from submit to teams, solve included (42 s of fan-out, on
the Job).

**Refined teams** (37 teams, 250 iterations, KQM standard substats):

| Shards | Total |
| :- | :- |
| 16 fixed shards, site run | 4 min 1 s (workers busy 54-238 s: some drew three slow teams) |
| 16 fixed shards, benchmark | 1 min 55 s |
| One per team (37), benchmark, same teams | 1 min 44 s (each team 25-60 s on 8 cores) |

A refined run is now bounded by its slowest team on one 8-vCPU instance (the
most Cloud Run gives one instance) plus start-up. 16 instances of 8 vCPU plus
the site fit Cloud Run's default regional quota of 200 vCPU; more workers
need a quota increase.

**GKE** (built 2026-11-26, not yet run live): workers are an Indexed Job on
a spot pool of c2d-highcpu-8 nodes that autoscales from zero. The project's
Compute Engine quota (32 vCPU in all regions) leaves room for 3 such workers,
against 16 on Cloud Run, and the cluster autoscaler takes 1-2 minutes to add
a node from zero. So it is expected to be slower: by estimate about 40-50 s of
simulating for the quick batch above and 6-7 minutes for the 37 refined teams.
