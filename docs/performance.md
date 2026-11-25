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
