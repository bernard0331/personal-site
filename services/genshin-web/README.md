# genshin-web

The team optimizer's website: a Go web server (standard library only) that serves
the pages and a small JSON API, and runs optimizations with the `genshin-calc`
packages. AGPL-3.0, like `genshin-calc`, whose code it links.

## Run it locally

From the repository root:

```bash
python3 services/genshin-web/tools/fetch_images.py      # once: icons into local/web-img
go build -o bin/genshin-web ./services/genshin-web
bin/genshin-web                                         # http://127.0.0.1:8080

It needs the simulator (`bin/kqmsim`), the KQM Sim Database cache, a weapon
ranking (`genshin-calc weapons`) and the HiGHS Python (`local/highs-venv`); see
`-h` for the paths. Runs and the shared cache go to `local/web-data/`.
```

## Layout

| Path | What it is |
| :- | :- |
| `main.go` | Starts the server and its routes; serves the embedded front end and the icons. |
| `catalog.go` | `GET /api/catalog`: every released character (Lumine as the Traveler), weapon and artifact set, with names and icon filenames. |
| `good.go` | `POST /api/good`: reads an uploaded GOOD file with the `genshin-calc/good` parser, which treats it as untrusted (size cap, every key and number checked). |
| `runs.go` | `POST /api/runs` and `GET /api/runs/{id}`: a run is a locked snapshot (characters, weapon copies, options) under a random private id. Every value is checked; the queue is bounded, so a flood of requests is turned away instead of piling up. Runs are files under `-data`. |
| `engine.go` | The background worker: one run at a time, it builds the account, picks rotations and weapons (`genshin-calc/optimizer`), simulates at 30 iterations through a shared cache (any configuration simulated once is never simulated again, for anyone), and has HiGHS choose the teams for all four breakdowns, pinned characters required. |
| `display.json` | English names and icon filenames for catalog keys. Generated; do not edit. |
| `web/` | The front end, embedded in the binary: `index.html`, `style.css`, `app.js` (plain JavaScript, no build step). The visitor's account is a draft in their browser's storage until they optimize. |
| `tools/gen_display.py` | Regenerates `display.json` from the genshin-db API when the simulator gains characters or weapons. |
| `tools/fetch_images.py` | Downloads the game's UI icons from Enka.Network into `local/web-img/`. The images belong to HoYoverse and are never committed. |
