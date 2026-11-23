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
| `canary/genshin-web/` | The canary copy of genshin-web's workload and the weighted, cookie-sticky route that `canary.sh` applies. |

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

The split is an HTTPRoute with weighted `backendRefs` (Gateway API). The
global load balancer does the splitting, and no service mesh is needed. Each
version sets a `track` cookie, and the route's first two rules send a
returning visitor to the version that first served them. Only visitors with
no cookie are split by weight. That stickiness is needed because each
version holds its run queue in memory and keeps its runs in its own bucket
folder (`gke/` and `gke-canary/`).

**Rollback** puts the base route back, sending everyone to stable, and
deletes the canary. Stable then moves the canary's visitors to its own
cookie. Runs that were in progress on the canary are lost.

**Promote** sends everyone to the canary while stable's Deployment is
replaced with the canary's image, then sends everyone back and deletes the
canary. Afterwards run `apply.sh` with the same image, so that the next apply
doesn't revert it.

The canary reserves a quarter of a CPU and 1 GiB, and runs one simulation at
a time, so it fits on the single spot node beside stable.
