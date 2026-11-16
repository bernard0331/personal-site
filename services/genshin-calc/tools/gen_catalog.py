#!/usr/bin/env python3
"""Generate catalog/catalog.json from a KQM Sim source checkout.

The optimizer needs to know every character, weapon and artifact set the
simulator supports: its key, and for characters the element, weapon type and
rarity. KQM Sim keeps that in generated Go files under pkg/catalog. This script
reads those files and writes the few fields we need as JSON, so the Go service
does not depend on the simulator's source at build time.

Run it again whenever the pinned KQM Sim release changes:

    python3 tools/gen_catalog.py ../../../kqmsim

Standard library only.
"""
import json
import re
import subprocess
import sys
from pathlib import Path

ELEMENTS = {"Fire": "pyro", "Water": "hydro", "Electric": "electro", "Ice": "cryo",
            "Wind": "anemo", "Rock": "geo", "Grass": "dendro"}
WEAPON_TYPES = {"WEAPON_SWORD_ONE_HAND": "sword", "WEAPON_CLAYMORE": "claymore", "WEAPON_POLE": "polearm",
                "WEAPON_BOW": "bow", "WEAPON_CATALYST": "catalyst"}
QUALITY = {"QUALITY_ORANGE": 5, "QUALITY_ORANGE_SP": 5, "QUALITY_PURPLE": 4}


def blocks(path):
    """Yield the text of each `keys.Name: { ... }` entry in a catalog file."""
    text = path.read_text()
    starts = [m.start() for m in re.finditer(r"^\tkeys\.\w+: \{$", text, re.M)]
    for begin, end in zip(starts, starts[1:] + [len(text)]):
        yield text[begin:end]


def field(block, pattern):
    m = re.search(pattern, block)
    return m.group(1) if m else None


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    sim = Path(sys.argv[1])
    here = Path(__file__).resolve().parent.parent
    unreleased = {line.strip() for line in (here / "catalog" / "unreleased.txt").read_text().splitlines()
                  if line.strip() and not line.startswith("#")}

    characters = []
    for b in blocks(sim / "pkg/catalog/character.dm.go"):
        key = field(b, r'Key:\s+"(\w+)"')
        characters.append({
            "key": key,
            "element": ELEMENTS[field(b, r"ElementType_(\w+)")],
            "weapon_type": WEAPON_TYPES[field(b, r"WeaponType_(\w+)")],
            "rarity": QUALITY[field(b, r"QualityType_(\w+)")],
            "released": key not in unreleased,
        })
    weapons = []
    for b in blocks(sim / "pkg/catalog/weapon.dm.go"):
        weapons.append({
            "key": field(b, r'Key:\s+"(\w+)"'),
            "weapon_type": WEAPON_TYPES[field(b, r"WeaponType_(\w+)")],
            "rarity": int(field(b, r"Rarity:\s+(\d)")),
        })
    sets = [field(b, r'Key:\s+"(\w+)"') for b in blocks(sim / "pkg/catalog/artifact.dm.go")]

    # Rotation scripts may call a character, weapon or set by a short name ("bina",
    # "favlance", "no"). Record every short name with the full key it stands for.
    def aliases(path, known):
        out = {}
        for alias, ident in re.findall(r'^\t"(\w+)":\s+keys\.(\w+),$', (sim / path).read_text(), re.M):
            key = ident.lower()
            if key not in known:
                sys.exit(f"{path}: short name {alias!r} points to {key!r}, which is not in the catalog")
            if alias != key:
                out[alias] = key
        return dict(sorted(out.items()))

    shortcuts = {
        "characters": aliases("pkg/shortcut/character.dm.go", {c["key"] for c in characters}),
        "weapons": aliases("pkg/shortcut/weapon.dm.go", {w["key"] for w in weapons}),
        "artifact_sets": aliases("pkg/shortcut/artifact.dm.go", set(sets)),
    }

    missing = unreleased - {c["key"] for c in characters}
    if missing:
        sys.exit(f"unreleased.txt names characters the simulator does not have: {sorted(missing)}")

    commit = subprocess.run(["git", "-C", str(sim), "rev-parse", "HEAD"], capture_output=True, text=True).stdout.strip()
    tag = subprocess.run(["git", "-C", str(sim), "describe", "--tags", "--abbrev=0"], capture_output=True, text=True).stdout.strip()
    patches = sorted(p.name for p in (here / "simulator" / "patches").glob("*.patch"))
    if patches:
        tag += f"+{len(patches)}patches"
    out = {
        "engine": {"name": "kqmsim", "release": tag, "commit": commit},
        "characters": sorted(characters, key=lambda c: c["key"]),
        "weapons": sorted(weapons, key=lambda w: w["key"]),
        "artifact_sets": sorted(sets),
        "shortcuts": shortcuts,
    }
    (here / "catalog" / "catalog.json").write_text(json.dumps(out, indent=1) + "\n")
    print(f"{len(characters)} characters ({len(unreleased)} unreleased), {len(weapons)} weapons, {len(sets)} artifact sets, engine {tag or commit[:7]}")


if __name__ == "__main__":
    main()
