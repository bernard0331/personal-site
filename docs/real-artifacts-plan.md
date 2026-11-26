# Plan: optimizing with the player's own artifacts

Status: planned (2026-11-26), not built. Part of the original scope ("real
mode": allocate pieces from the whole inventory, like weapons), scheduled as
phase 2b. Today every run gives characters KQM-standard or top-1% substats
from KQM Sim's substat optimiser; the imported artifacts are only shown.

## The problem

After the teams are chosen, give each of their characters five pieces (one
per slot) from the player's inventory, so that:

- no piece is used twice across all chosen teams (like weapon copies);
- each character gets a set layout that works (4-piece, 2+2, or the
  rotation's own);
- each character reaches the Energy Recharge it needs to burst on time;
- the teams' DPS, judged the way the breakdown ranks them (best weakest team,
  or highest average), is as high as possible.

What makes it hard: there are no damage formulas here, only the simulator.
And a piece's value depends on the whole team, not only its wearer (the
prototype's Fischl did best on Tenacity, a team buff, not her own damage).

## Method

The four stages of the prototype (`local/opt/fischl_opt.py`, one character),
turned into a pipeline for every character of every chosen team:

1. **Choose teams as today.** The quick and refined passes run with standard
   artifacts and pick each breakdown's teams. (Decision 1 below.)
2. **Measure, per character, in its team:**
   - *ER need:* sweep extra ER with energy on, and take the least at which
     the character never waits for energy.
   - *Stat values:* with energy ignored, add a fixed amount of each relevant
     stat (ATK%, flat ATK, CRIT Rate, CRIT DMG, Elemental Mastery, its
     element's DMG%, HP% or DEF% where it scales) and measure the **team's**
     DPS gain per point. 2,000 iterations each, so the noise stays below the
     differences.
   - *Set layouts:* simulate the few layouts worth trying: the rotation's
     own, the character's most common layouts in the rotation database, and
     2+2 combinations of those.
3. **Allocate all characters at once** as an integer program, solved with
   HiGHS (already used for team selection):
   - a variable per (character, piece), after pruning to each slot's best
     ~15 pieces by score plus the best ER pieces;
   - one piece per slot per character, each piece at most once;
   - a set-layout choice per character, with the set counts it needs;
   - the ER floor per character;
   - the objective: the sum of measured stat values plus each layout's
     measured worth, combined across teams the way the breakdown ranks
     (sum, or maximise the weakest team).
4. **Correct and check by simulation.** Stat values are only linear near
   where they were measured (CRIT Rate and CRIT DMG multiply), so measure
   them again at the allocated build and re-solve once. Then simulate every
   chosen team on its real gear at 1,000 iterations. Those numbers are the
   ones shown.

Per-piece **Exclude** (already on the Artifacts step) removes a piece from
the inventory. Main stats use the piece's actual level (KQM Sim's
`artifact_main_gen.json` table), so unlevelled pieces count for what they
are. The solver assumptions say "artifacts are whatever the user submitted".

## How it fits into runs

- A third choice for refined runs' artifacts: **KQM standard, top 1%, or my
  artifacts**. "My artifacts" needs a refined run, because it builds on the
  refined teams.
- The snapshot carries the artifact list and the excluded pieces, with the
  same size caps and checks as the GOOD import.
- Results show each member's five pieces (set, main stat, substats, where it
  is now), plus a "what to equip" list ordered by character. That's the part
  a player acts on.
- The measuring simulations are plain simulator runs, so they go out through
  the fan-out (quick kind) unchanged; the allocation runs on the site.

## Cost and time (estimates, Cloud Run with 16 workers)

| Stage | Work for 4 breakdowns, about 40-60 distinct (character, team) pairs | Time |
| :- | :- | :- |
| ER sweep, stat values, layouts | about 15-20 simulations per pair at 1,000-2,000 iterations: about 25,000-35,000 vCPU-seconds | about 3-5 min |
| Allocation (HiGHS) | a few thousand variables, four problems | seconds |
| Re-measure at the allocated build | about half the first measurement | about 2 min |
| Check every chosen team on real gear | about 100 team simulations | under a minute |
| **On top of a refined run** | | **about 5-10 min** |

Without the fan-out (the site's 7 cores alone) the same work is about 1-1.5
hours. On Cloud Run's prices that's roughly $0.75-1.50 of compute per run
beyond the free tier.

## Decisions to make before building

1. **Teams chosen first, then gear (recommended to start), or teams chosen
   with real gear?** Choosing first is cheaper and simple. But a team that
   loses on standard artifacts may win with the player's real pieces. A later
   step could gear the top 2-3 alternatives per element too and let the
   solver pick again.
2. **ER: hard floor or soft?** A hard floor (never wait for energy) is safe
   and matches how the site already distrusts teams that wait. A soft floor
   lets the solver trade a little waiting for damage.
3. **Keep current gear?** Optionally lock a piece to its current wearer, as
   the approved design had ("lock to wearer"), for players who don't want to
   reshuffle everything.
4. **Which breakdowns?** All four, or only the one the player looks at
   first, to save time.

## Build order

1. Go port of the prototype for one team: ER sweep, stat values, layouts,
   piece selection, final simulation. Check it reproduces the prototype on
   the owner's account (Fischl's team: Elegy R1 with 4-piece Tenacity at
   about 263k DPS, against 256k equipped).
2. The multi-team allocation in HiGHS, checked on the owner's account:
   every piece used at most once, every ER floor met, real-gear DPS by
   simulation.
3. The snapshot, the run mode and the results page (pieces per member,
   "what to equip"), and the page wording fixed to match.
4. Measure a real run end to end and record it in `docs/performance.md`.
