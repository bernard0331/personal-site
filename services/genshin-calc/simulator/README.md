# The simulator

genshin-calc scores teams with [KQM Sim](https://github.com/KQM-git/kqmsim),
KeqingMains' fork of [gcsim](https://github.com/genshinsim/gcsim), at the release
pinned in `build.sh`, plus the patches in `patches/`.

Both projects are licensed under AGPL-3.0, and so are these patches.

## Why there are patches

KQM Sim follows gcsim, but not immediately. When gcsim has merged a change that
matters for the optimizer and KQM Sim's latest release does not have it yet, the
change is applied here as a patch. Each patch is gcsim's own commit, unchanged
except where it had to be fitted to KQM Sim's code; the commit message records
the original with `cherry picked from commit ...`.

| Patch | gcsim pull request | What it changes |
| :- | :- | :- |
| 0001 | #3212 Add Wrio Revelation | Wriothesley's Stellar-Conduct upgrade. One conflict fitted: both projects add attack tags in the same place, so all four are kept. |
| 0002 | #3250 Add Cyno Revelation | Cyno's Stellar-Conduct upgrade |
| 0003 | #3263 Prevent Cyno from having simultaneous a1 tickers | Fix that came with the upgrade |
| 0004 | #3264 Fix Cyno unable to CA during Q | Fix that came with the upgrade |
| 0005 | #3266 Prevent Cyno from having simultaneous A1 tickers try 2 | Fix that came with the upgrade |

The upgrades only take effect inside a Polestar Field, which appears when a
Stellar character (Odette, Sandrone, Mitya, Danica, Anastasya or the Cryo
Traveler) is in the party and Electro meets Cryo. In other teams Wriothesley and
Cyno play as before.

When a new KQM Sim release includes a patch's change, move the pin in `build.sh`
forward and delete that patch.

## Building

```bash
services/genshin-calc/simulator/build.sh
```

This writes `bin/kqmsim` at the repository root. If the release changed,
regenerate the catalog from a checkout of the same source:

```bash
python3 services/genshin-calc/tools/gen_catalog.py PATH_TO_A_KQMSIM_CHECKOUT
```
