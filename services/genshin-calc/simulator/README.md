# The simulator

genshin-calc scores teams with [KQM Sim](https://github.com/KQM-git/kqmsim),
KeqingMains' fork of [gcsim](https://github.com/genshinsim/gcsim), at the release
pinned in `build.sh`, plus the patches in `patches/`.

Both projects are licensed under AGPL-3.0, and so are these patches.

## Why there are patches

KQM Sim follows gcsim, but not immediately. When gcsim has merged a change that
matters for the optimizer and KQM Sim's latest release does not have it yet, the
change is applied here as a patch. Patches 0001 to 0005 are gcsim's own commits, unchanged
except where they had to be fitted to KQM Sim's code; each commit message records
the original with `cherry picked from commit ...`. Patches 0006 and 0007 are this project's own
additions: an opt-in behaviour that changes nothing unless a config asks for it, and a
weapon the release lacks.

| Patch | gcsim pull request | What it changes |
| :- | :- | :- |
| 0001 | #3212 Add Wrio Revelation | Wriothesley's Stellar-Conduct upgrade. One conflict fitted: both projects add attack tags in the same place, so all four are kept. |
| 0002 | #3250 Add Cyno Revelation | Cyno's Stellar-Conduct upgrade |
| 0003 | #3263 Prevent Cyno from having simultaneous a1 tickers | Fix that came with the upgrade |
| 0004 | #3264 Fix Cyno unable to CA during Q | Fix that came with the upgrade |
| 0005 | #3266 Prevent Cyno from having simultaneous A1 tickers try 2 | Fix that came with the upgrade |
| 0006 | Our own | Mizuki's pull-out tech as an option, `+params=[pullout=1]`: her third skill hit lands just after Witch's Revelation's cooldown, moving her big hits from skill hits 1, 6, 10, 14 to 1, 4, 8, 12. From "The Ultimate Mizuki Guide (Part 1)" (YouTube, Nb_Vbwdm0MI). Off by default. |
| 0007 | Our own | Adds the 4-star sword Silver Light (Odette's weapon in the owner's account). Stats and passive from lunaris.moe, game data 7.2.0. |

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

## The seeded variant

`patches-seeded/` holds one more patch of our own, built only into a second
binary, `kqmsim-seeded` (`build.sh OUTPUT patches-seeded`). It adds `-seed N`:
iteration i runs with seed N+i instead of a random one. Two configs run with
the same seed see the same random rolls, so their difference is the change
alone. The substat optimiser already compares configs this way inside KQM Sim
(`RunWithSeededConfig`); the flag exposes it for measuring what each stat or
artifact set is worth to a team (real-artifact runs,
[docs/real-artifacts-plan.md](../../../docs/real-artifacts-plan.md)). The
main `kqmsim` binary doesn't change, so the shared cache keyed on it keeps
its results.
