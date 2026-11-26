# genshin-web on Cloud Run

The team optimizer runs as one Cloud Run service: this is the live site. A
GKE version (cluster, Gateway, Terraform) is also defined in this repository
for practice, but is switched off; see [terraform/README.md](../../terraform/README.md).

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

A refined run is a quick run, then about 120 of its most promising teams
again: the quick run's picks, the best of each element and each character's
best team. KQM Sim's substat optimiser chooses their substats, and the result
is simulated with 250 iterations (the command line's full pass uses 1,000).
The quick teams show while it runs. There is one level since 2026-11-26
(light and thorough were removed); its settings are `standardRefine` in
services/genshin-web/refine.go.

Each refined team goes to its own worker (see the fan-out in the main
README). A standard run on the owner's account with nothing refined cached
took 6 min 19 s on 2026-11-26: 129 teams in 5 min 41 s on 16 workers (a team
takes 26 s at the median, 190 s at worst). Before the fan-out the site alone
took about 25 minutes.

## Limits

One run at a time and 6 runs an hour per visitor (IP address), and at most 20
runs waiting. More visitors make the queue longer; they cannot add instances.
A refined run holds the queue for about 6 minutes; everyone else waits.

## Cost

Nothing while no instance is up. While one is, Cloud Run bills 8 vCPU and
8 GiB per second: a run from scratch takes about 5 minutes (about 4 of
simulating), one with everything cached under a minute, and an idle
instance stays up to about 15 minutes after the last visit. At about $0.58
an instance-hour beyond the free tier. The workers bill only while they
simulate: a refined run with nothing cached kept them busy for about 33,000
vCPU-seconds, about $0.85 beyond the free tier (2026-11-26). It costs more
than the site alone did (about $0.25), because a team on 8 cores finishes far
sooner but uses more CPU-seconds in all. Cloud Run's monthly free tier
(180,000 vCPU-seconds) covers a few such runs. The bucket and the registry cost
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
