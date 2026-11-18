#!/usr/bin/env python3
"""Generate display.json: English names and icon filenames for the catalog.

The simulator's catalog (services/genshin-calc/catalog/catalog.json) has keys,
elements, rarities and weapon types, but no display names or pictures. This
tool asks the genshin-db API (genshin-db-api.vercel.app, theBowja's open
Genshin data) for every character, weapon and artifact set, matches them to
catalog keys by name, and writes each one's English name and icon filenames.
The icons themselves are the game's UI images; tools/fetch_images.py
downloads them. Only names and filenames are committed, no images.

usage: gen_display.py   (run from the repository root; needs network)
"""
import json, re, sys, urllib.parse, urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
CATALOG = ROOT / "services/genshin-calc/catalog/catalog.json"
OUT = ROOT / "services/genshin-web/display.json"
API = "https://genshin-db-api.vercel.app/api/v5/"


def get(path, **q):
    q.setdefault("resultLanguage", "English")
    url = API + path + "?" + urllib.parse.urlencode(q)
    req = urllib.request.Request(url, headers={"User-Agent": "personal-site gen_display"})
    return json.load(urllib.request.urlopen(req, timeout=30))


def norm(name):
    """A display name in catalog-key form: lowercase letters and digits only."""
    return re.sub(r"[^a-z0-9]", "", name.lower())


def main():
    cat = json.load(open(CATALOG))
    out = {"characters": {}, "weapons": {}, "artifact_sets": {}}
    missing = []

    # Characters. The Travelers are one entry per twin in genshin-db; every
    # player is assumed to be Lumine, whose icon is "PlayerGirl".
    names = {norm(n): n for n in get("characters", query="names", matchCategories="true")}
    for c in cat["characters"]:
        k = c["key"]
        if k.startswith(("aether", "lumine")):
            out["characters"][k] = {"name": "Traveler (" + c["element"].capitalize() + ")",
                                    "icon": "UI_AvatarIcon_PlayerGirl"}
            continue
        n = names.get(k) or names.get(norm(k))
        if not n:
            missing.append("character " + k); continue
        d = get("characters", query=n)
        out["characters"][k] = {"name": d["name"], "icon": d["images"].get("filename_icon")}

    names = {norm(n): n for n in get("weapons", query="names", matchCategories="true")}
    for w in cat["weapons"]:
        n = names.get(w["key"])
        if not n:
            missing.append("weapon " + w["key"]); continue
        d = get("weapons", query=n)
        out["weapons"][w["key"]] = {"name": d["name"], "icon": d["images"].get("filename_icon")}

    names = {norm(n): n for n in get("artifacts", query="names", matchCategories="true")}
    for s in cat["artifact_sets"]:
        n = names.get(s)
        if not n:
            missing.append("set " + s); continue
        d = get("artifacts", query=n)
        im = d.get("images", {})
        out["artifact_sets"][s] = {"name": d["name"], **{slot: im.get("filename_" + slot)
                                   for slot in ("flower", "plume", "sands", "goblet", "circlet")}}

    json.dump(out, open(OUT, "w"), indent=1, sort_keys=True)
    print(f"wrote {OUT.relative_to(ROOT)}: {len(out['characters'])} characters, "
          f"{len(out['weapons'])} weapons, {len(out['artifact_sets'])} sets")
    if missing:
        print(f"{len(missing)} catalog entries without a match:", ", ".join(missing))


if __name__ == "__main__":
    sys.exit(main())
