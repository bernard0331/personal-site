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
| `main.go` | The command-line program: `inspect` a GOOD file, print the simulator `config` for a player's version of a team (`-four-stars` assumes R1 of every 4-star weapon the file leaves out, since scans often miss them), and `check-rotations` in a cached database file. |
| `catalog/` | Every character, weapon and artifact set the pinned simulator supports, embedded as `catalog.json`. `unreleased.txt` lists characters that are never calculated. |
| `good/` | Reads and checks a GOOD file. Nothing from the file is trusted: unknown keys and out-of-range values are dropped and reported. |
| `rotation/` | Splits a rotation file into its script and its example builds, and writes the config for a player's own constellations, levels and weapons. |
| `rotations/` | Rotations written for this project, for teams the KQM Sim Database lacks. Each names its source. |
| `tools/gen_catalog.py` | Regenerates `catalog/catalog.json` from a KQM Sim source checkout. Run it when the pinned release changes. |

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
