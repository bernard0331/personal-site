# genshin-web on Cloud Run

The team optimizer runs as one Cloud Run service while friends test it. This
is the design doc's "cheaper fallback": the GKE cluster, Gateway and
Terraform (build phases 3 and 4) come later and replace it.

## What exists

All in one project and region (`us-central1`); names other than the service's
are set by environment variables, never stored in this repository.

| Resource | What it is for |
| :- | :- |
| Cloud Run service `genshin-web` | The site. One instance at most (it holds the run queue and the per-visitor limits in memory, and it caps the bill), none while idle, 8 vCPU and 8 GiB while up. CPU stays on between requests because runs continue after the request that started them. Public: the site has no sign-in. |
| Artifact Registry repository `genshin` | The container images, tagged with the commit they were built from. |
| Cloud Storage bucket (`BUCKET`) | Mounted at `/data`: each run's snapshot, status and result (`runs/`, deleted after 90 days by a lifecycle rule) and a copy of the shared simulation cache. Not public. |
| Service account `genshin-web` | What the service runs as. Its only permission is reading and writing objects in that bucket. No keys exist. |

The image holds everything else: the site and the patched simulator (built
from source for Linux), Python with HiGHS, this repository's rotation files,
and three local-only inputs that are never committed: the cached KQM Sim
Database, the weapon ranking, and HoYoverse's images.

## Why the cache is on the instance

The shared cache gains one line per simulation, thousands per run. A Cloud
Storage mount uploads the whole file on every write, so the cache lives on
the instance's own disk (`-cache /tmp/cache.jsonl`) and is copied to the
bucket after every run (`-cache-copy /data/cache.jsonl`). A new instance
restores it from that copy; during a run it is also copied every 10 minutes.

Cloud Run stops an instance about 15 minutes after the last request, even
mid-run (the run page asks for progress every few seconds, so this happens
only when nobody has it open). The next instance queues such runs again
(those under two days old) and they continue from the cache, so a refined
run left alone pauses rather than fails, until someone opens the site.

## Updating the site

```bash
deploy/genshin-web/build.sh
```

```bash
BUCKET=<the bucket> deploy/genshin-web/deploy.sh <the image build.sh printed>
```

`build.sh` assembles a build folder outside the repository and builds it with
Cloud Build (about 4 minutes). `deploy.sh` creates a new revision and moves
all traffic to it; runs and the cache survive in the bucket.

## Refined runs

A refined run is a quick run, then its most promising teams simulated again
with KQM Sim's substat optimiser and 1,000 iterations each (the command
line's full pass). The quick teams show while it runs. Levels: Light (the
quick run's picks and about 15 to 35 more teams, ~10 minutes), Standard
(also each character's best team, ~120 teams, ~25 minutes) and Thorough
(the full pass's 100 / 20 per element / 3 per character, adjustable; about
an hour). A light run on a 12-character account took 3 minutes on Cloud Run
(2026-11-22), quick pass included. Settings live in
`refineLevels` in services/genshin-web/refine.go.

## Limits

One run at a time and 6 runs an hour per visitor (IP address), and at most 20
runs waiting. More visitors make the queue longer; they cannot add instances.
A thorough run holds the queue for about an hour; everyone else waits.

## Cost

Nothing while no instance is up. While one is, Cloud Run bills 8 vCPU and
8 GiB per second: a run from scratch takes about 5 minutes (about 4 of
simulating), one with everything cached under a minute, and an idle
instance stays up to about 15 minutes after the last visit. At about $0.58
an instance-hour beyond the free tier, a light refined run costs roughly
$0.10, a standard one $0.25 and a thorough one $0.60. Cloud Run's
monthly free tier covers light testing. The bucket and the registry cost
cents a month. Check the project's budget alerts and the billing report.

## Taking it down

```bash
gcloud run services delete genshin-web --region us-central1
```

Deleting the bucket and the registry removes saved runs and images too, so do
that only when they are no longer wanted.

## Notes

- Rebuilding the same code gives the same simulator binary (build.sh pins
  patch dates, -trimpath and the exact Go image), so a redeploy keeps the
  shared cache. Changing the simulator itself starts the cache over.
- Each HiGHS solve runs on one thread: the four breakdowns are solved at
  once, and HiGHS's own thread pool oversubscribed the instance (2+ minutes
  a solve instead of 10-20 s).

- `/healthz` returns 404 on Cloud Run: Google's front end reserves some paths
  ending in "z". Cloud Run checks the port itself, so nothing uses it there.
