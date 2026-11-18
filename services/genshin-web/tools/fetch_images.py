#!/usr/bin/env python3
"""Download the game's UI icons listed in display.json.

Images belong to HoYoverse; they are not committed. They come from Enka.Network's
public mirror of the game's UI images (enka.network/ui/<filename>.png) and go to
local/web-img/ (git-ignored), which the web server serves at /img/. Files already
downloaded are skipped, so rerunning only fetches new ones.

usage: fetch_images.py [DIR]   (run from the repository root; default local/web-img)
"""
import json, sys, urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
DISPLAY = ROOT / "services/genshin-web/display.json"


def main():
    out = Path(sys.argv[1]) if len(sys.argv) > 1 else ROOT / "local/web-img"
    out.mkdir(parents=True, exist_ok=True)
    d = json.load(open(DISPLAY))
    names = {v["icon"] for kind in ("characters", "weapons") for v in d[kind].values() if v.get("icon")}
    names |= {v[s] for v in d["artifact_sets"].values() for s in ("flower", "plume", "sands", "goblet", "circlet") if v.get(s)}
    got = skipped = failed = 0
    for n in sorted(names):
        p = out / f"{n}.png"
        if p.exists():
            skipped += 1; continue
        try:
            req = urllib.request.Request(f"https://enka.network/ui/{n}.png", headers={"User-Agent": "personal-site fetch_images"})
            b = urllib.request.urlopen(req, timeout=30).read()
            if b[:4] != b"\x89PNG":
                raise ValueError("not a PNG")
            p.write_bytes(b); got += 1
        except Exception as e:
            failed += 1; print("failed", n, e)
    print(f"{got} downloaded, {skipped} already there, {failed} failed -> {out}")


if __name__ == "__main__":
    main()
