# genshin-calc

The simulation and solver service of the Genshin team optimizer. Given a player's
account, it finds the strongest teams with no character and no weapon copy used
twice: seven teams, one for each element, and ten teams with no element rule.

Team damage comes from [KQM Sim](https://github.com/KQM-git/kqmsim), KeqingMains'
fork of [gcsim](https://github.com/genshinsim/gcsim). This service writes no damage
formulas of its own.

## License

KQM Sim and gcsim are licensed under AGPL-3.0, so this service is too. See
[LICENSE](LICENSE). The rest of the repository is not covered by that file.

## The simulator

The service runs KQM Sim as a command-line program: release `kqmsim-v7.3.3` plus
a few patches from upstream gcsim that the release does not have yet, such as
Wriothesley's and Cyno's Stellar-Conduct upgrades. See
[simulator/README.md](simulator/README.md). Every result records the version that
produced it.

Build it into `bin/kqmsim` at the repository root:

```bash
services/genshin-calc/simulator/build.sh
```

Run one config and write the full result as JSON:

```bash
./bin/kqmsim -c path/to/config.txt -out path/to/result.json
```

The result's `statistics.dps.mean` is the team's DPS, and `statistics.element_dps`
is the damage split by element, which decides the team's element.

## Layout

| Path | What it is |
| :- | :- |
| `main.go` | The command-line program: `inspect` a GOOD file, print the simulator `config` for a player's version of a team (`-four-stars` assumes R1 of every 4-star weapon the file leaves out, since scans often miss them; `-three-stars` assumes every 3-star weapon at R5 in unlimited copies), `check-rotations` in a cached database file, and run the `coarse` pass. |
| `coarse_cmd.go`, `coarse/` | The coarse pass: simulate every single-target rotation a player can field (KQM Sim Database cache plus `rotations/`) on a pool of worker goroutines, and append each result as a JSON line to a cache. Cached results are skipped, so a stopped run resumes. Results flag rotations that wait for energy or cooldowns, and Iansan rotations that report no movement. |
| `full_cmd.go`, `coarse/full.go` | The full pass: take the most promising teams from the coarse pass (best overall, best per main element, best per character, so the solver has teams for everyone), let KQM Sim's substat optimiser choose their substats (KQM standard or top 1%), and simulate the result with 1,000 iterations. Results keep the chosen substat lines. |
| `weapons_cmd.go`, `weapons/` | Weapon allocation. The weapon trials try each character's candidate weapons (every owned 5-star of the type, the weapons the rotations give that character most often, and the rotation's own) in two of the character's most common teams, and rank them by team DPS. The passes then give each member their best-ranked free copy; members compete for a copy by how much they would lose without it. |
| `solver/` | Chooses the best set of teams with no character or weapon copy used twice: one team per element, or any N teams, each by best weakest team or highest average. The main solver is HiGHS, an open-source integer programming solver (MIT licence), run through `highs_solve.py` (embedded in the binary); it proves its answers best over every team and weapon option in about a minute each. A slower branch and bound in Go, checked against brute force, is the fallback without HiGHS and the reference the HiGHS tests compare against. |
| `report_cmd.go` | Runs the solver on a pass's results and prints both breakdowns as Markdown, with each team's DPS per member, weapons, rotation and flags (`-stats` adds the optimised substats). |
| `catalog/` | Every character, weapon and artifact set the pinned simulator supports, embedded as `catalog.json`. `unreleased.txt` lists characters that are never calculated. |
| `good/` | Reads and checks a GOOD file. Nothing from the file is trusted: unknown keys and out-of-range values are dropped and reported. |
| `rotation/` | Splits a rotation file into its script and its example builds, and writes the config for a player's own constellations, levels and weapons. |
| `rotations/` | Rotations written for this project, for teams the KQM Sim Database lacks. Each names its source. |
| `tools/gen_catalog.py` | Regenerates `catalog/catalog.json` from a KQM Sim source checkout. Run it when the pinned release changes. |

The whole pipeline on a scan (run from the repository root; every step caches its
simulations, so Ctrl-C stops it and running the same command again continues):

```bash
go build -o bin/genshin-calc ./services/genshin-calc
A="-four-stars -three-stars -weapons local/weapons/ranking.json"
DB=local/rotations/kqm-db.jsonl
bin/genshin-calc weapons -four-stars -three-stars $DB path/to/scan.json  # ~2,100 trials, ~12 min
bin/genshin-calc coarse $A -workers 4 $DB path/to/scan.json             # ~4,600 rotations, 100 iterations
bin/genshin-calc full $A $DB path/to/scan.json                          # ~300 teams, ~40 s each
bin/genshin-calc report $A local/full/kqm.jsonl path/to/scan.json
```

The report solves with HiGHS when `local/highs-venv` exists. Set it up once
(HiGHS's official Python package; Homebrew does not build HiGHS on macOS 15):

```bash
python3 -m venv local/highs-venv && local/highs-venv/bin/pip install highspy
```

The weapon options must be the same for `coarse` and `full`, because `full`
rebuilds the configs. `-dry-run` lists what a step would simulate. In `full`,
`-mode top1` uses top-1% substats instead of KQM standard; `-top`,
`-per-element` and `-per-character` set how many teams it takes (defaults 100,
20, 3). The report can also read the coarse results, to see a breakdown before
the full pass.

Run the tests and try it on a scan:

```bash
go test ./services/genshin-calc/...
go run ./services/genshin-calc inspect path/to/scan.json
```

## Local data

Account scans, cached rotations and simulator results go in `local/`, which git
ignores. Rotations fetched from the [KQM Sim Database](https://db.kqm.gg) are
credited to their submitters and are not committed. Rotations written for this
project will live in this service's folder with their source credited.
