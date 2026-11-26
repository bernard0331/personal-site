# Plan: optimizing with the player's own artifacts

Status: planned and decided (2026-11-26), not built. Part of the original scope ("real
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

1. **Pick contenders as today.** The quick and refined passes run with
   standard artifacts; their best teams become the contenders that get
   geared (Decision 1 below).
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

## Decisions (owner, 2026-11-26)

1. **Teams are chosen with real gear**, among contenders, for accuracy:
   1. Gear each contender team on its own, as if it had the whole inventory.
   2. Choose all four breakdowns' teams from those numbers.
   3. Allocate jointly for the chosen teams, so each piece has one wearer.
   4. Choose again with the corrected numbers, until the choice stops changing
      (usually 2-3 rounds; each round re-gears only the teams that changed).

   Contenders are the best few teams per element, plus any team within about
   10% of the weakest chosen team on standard artifacts. Refined runs have one
   level since 2026-11-26 (standard, about 120 teams). Estimates with 16 Cloud
   Run workers:

   | Teams geared | Time on top of a refined run | Compute |
   | :- | :- | :- |
   | Only the chosen teams | 5-10 min | about $1 |
   | Every refined candidate (about 120) | about 40 min | about $7 |
   | Contenders (chosen: about 40-60) | 15-20 min | about $3-4 |

   Gearing one team (four characters measured in it) is about 2,400
   vCPU-seconds. Costs are before Cloud Run's free tier.
2. **Energy Recharge is a hard floor:** a character never waits for energy.
   The floor is measured under the run's energy setting, so generous energy
   drops already give a softer floor.
3. **No locking to the current wearer for now.** Exclude keeps a piece out.
4. **All four breakdowns** get their own allocation. Measurements are shared
   where the same character is in the same team.

## Build order

1. Go port of the prototype for one team: ER sweep, stat values, layouts,
   piece selection, final simulation. Check it reproduces the prototype on
   the owner's account (Fischl's team: Elegy R1 with 4-piece Tenacity at
   about 263k DPS, against 256k equipped).
2. Gearing contenders on their own, then the joint allocation in HiGHS and the
   choose-allocate-choose rounds, checked on the owner's account: every piece
   used at most once, every ER floor met, real-gear DPS by simulation.
3. The snapshot, the run mode and the results page (pieces per member,
   "what to equip"), and the page wording fixed to match.
4. Measure a real run end to end and record it in `docs/performance.md`.
