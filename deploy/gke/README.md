# deploy/gke

`apply.sh` deploys `deploy/overlays/prod` to the GKE cluster that
`terraform/envs/prod` creates. See `terraform/README.md` for bringing the
cluster up, cost, running it alongside Cloud Run and taking it down.

| Manifest | What it is |
| :- | :- |
| `base/gateway/` | The `gateway` namespace, the shared Gateway (global external Application Load Balancer on the reserved IP, TLS from the certificate map) and the HTTP to HTTPS redirect. |
| `base/genshin-web/` | The `genshin` namespace, the pod's service account (Workload Identity), the Deployment (one replica, the runs bucket mounted at /data), its Service, the load balancer's health check and the HTTPRoute for the site's hostname. |
| `overlays/prod/` | Both bases and the image. Placeholders that would contain the project id are filled in by `apply.sh` from Terraform's outputs. |
| `components/network-isolation/` | Default-deny NetworkPolicies and their allow rules (load balancer in; kube-dns, metadata server and private.googleapis.com out), included by every site. |
| `canary/genshin-web/` | The canary copy of genshin-web's workload and the weighted route, sticky per run through an `X-Track` header, that `canary.sh` applies. |

## Canary releases

`canary.sh` tries a new image on a share of new visitors before it replaces
the stable version:

```bash
deploy/gke/canary.sh start <new image> 10   # 10% of new visitors
deploy/gke/canary.sh status                 # versions, pods, split
deploy/gke/canary.sh weight 50              # widen it
deploy/gke/canary.sh promote                # the new image becomes stable
deploy/gke/canary.sh rollback               # or: everyone back to stable
```

The split is an HTTPRoute with weighted `backendRefs` (Gateway API), so
the global load balancer splits the traffic with no service mesh.

Runs are sticky. Each version holds its run queue in memory and keeps its
runs in its own bucket folder (`gke/`, `gke-canary/`), so a run can be
followed only on the version that started it:

- Every response names its version in an `X-Track` header.
- The page remembers each run's version, and sends `X-Track` back whenever
  it asks about that run.
- The route's first two rules match `X-Track: canary` and `X-Track: stable`
  exactly. Everything else (page loads, new runs) is split by weight.

On the live cluster, with the route programmed correctly, about 3 in 100
requests carrying `X-Track: canary` still reached stable, even after the
config had had well over 10 minutes to spread. So the page checks the
`X-Track` of every answer about a run, and asks again (up to five times)
when it came from the wrong version.

A header and not a cookie: matching a cookie needs a regular expression.
Google's load balancer allows only one per URL map, and only part of RE2.
A rejected route also blocks every other change to the shared Gateway until
it is fixed. Both of these were found on the live cluster.

**Rollback** puts the base route back, sending everyone to stable, and
deletes the canary. Runs that were in progress on the canary are lost.

**Promote** sends everyone to the canary while stable's Deployment is
replaced with the canary's image, then sends everyone back and deletes the
canary. Afterwards run `apply.sh` with the same image, so that the next apply
doesn't revert it.

Stable must already send `X-Track` before a canary starts (any image built
since the header was added). Otherwise runs started on stable carry no
header, and their polls may be split onto the canary.

The canary reserves a quarter of a CPU and 1 GiB, and runs one simulation at
a time. It fits on one spot node beside stable alone, but not once the
landing page (home) is also on that node; use `node_count = 2` for canary
sessions.

## Testing the SLO alerts

A canary with no ready pods does not test anything: the load balancer sends
its share to stable instead (checked live). `fault.yaml` runs
`tools/faultserver` with genshin-web's canary labels instead. It passes the
health check and answers 503 to every other request, so the route's weight
becomes the error rate:

```bash
IMG=REGION-docker.pkg.dev/PROJECT/genshin/faultserver:test   # built with Cloud Build from tools/faultserver
deploy/gke/canary.sh start <stable image> 50   # canary Service, health check, 50/50 route (waits for the canary pod: node_count = 2)
kubectl -n genshin scale deploy/genshin-web-canary --replicas=0
sed "s|IMAGE|$IMG|" deploy/gke/fault.yaml | kubectl apply -f -
# keep some traffic going; the fast-burn alert opens in about 6 minutes
kubectl -n genshin delete deploy genshin-web-fault
deploy/gke/canary.sh rollback
```

Alert openings and closings are logged in Cloud Logging
(`monitoring.googleapis.com/ViolationOpenEventv1` and `ViolationAutoResolveEventv1`),
so the test needs no notification channel.
