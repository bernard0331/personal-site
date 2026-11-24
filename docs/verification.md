# Verification log

What was tested on the live GKE environment, how, and what came out. The
environment is brought up only for these sessions and switched off after, so
each entry records the date it was last run. The scripts named here re-run the
checks on a fresh bring-up.

## 2026-11-24: SLOs and error-budget burn-rate alerts

Setup: GKE with 1 spot node, mesh off, both sites deployed (image 7a3a9b8),
steady test traffic of about 2 requests a second to genshin and 0.4 to www.

### Plan

| | |
| :- | :- |
| GKE off | `terraform plan`: no changes (nothing SLO-related is created) |
| GKE on | 18 monitoring resources: 2 custom services, 4 SLOs, 8 burn-rate policies, the 3 earlier alerts and the dashboard; applied cleanly |

### SLO data

| | |
| :- | :- |
| Site filter | the backend service names were `gkegw1-kna0-genshin-genshin-web-80-…` and `gkegw1-kna0-home-home-80-…`; the regex picks out each site |
| Compliance | all four SLOs returned data within minutes. genshin-web availability started at 93–95%: the first requests after bring-up got 5xx while the backend warmed up |
| Live p95 latency | 69 ms (genshin), 51 ms (home), so the 500 ms threshold has room |

The startup errors were a real check of the multi-window design: the
1-hour burn rate was above 14.4, but the 5-minute one was 0 by then, so
nothing fired.

### Forced errors

| Time (UTC) | Event |
| :- | :- |
| 05:19 | canary route at 50/50, canary scaled to zero pods: **0 errors**, the load balancer sent the canary's share to stable |
| 05:24 | `tools/faultserver` deployed as the canary (healthy, answers 503): about 45–55% of requests now 5xx |
| 05:30:13 | **slow-burn** alert opened (all three conditions: 6h and 30m burn above 6, at least 50 requests) |
| 05:31:22 | **fast-burn** alert opened; at about that time the burn rate was 28x over 1 hour and 100x over 5 minutes |
| 05:34:44 | the older PromQL "5xx error rate" alert opened |
| 05:38:28 | fault removed, `canary.sh rollback` |
| 05:51:04 | fast-burn alert resolved (the 5-minute window was clean; the 1-hour burn rate was still about 30) |
| 05:59:45 | PromQL 5xx alert resolved |
| 06:15:09 | slow-burn alert resolved |

Source: Cloud Logging, `monitoring.googleapis.com/ViolationOpenEventv1` and
`ViolationAutoResolveEventv1`. The latency SLO and home's alerts didn't
fire, as expected.

Found while testing:

- A canary with no ready pods doesn't produce errors at Google's load
  balancer: weighted traffic goes only to backends with healthy endpoints.
  Hence the fault server.
- The canary didn't fit on one node beside stable and home (insufficient
  CPU and memory); the fault server needs only 10m of CPU.

## 2026-11-23: NetworkPolicies, canary, alerts, service mesh, CI plan

Setup: GKE Standard, zonal, 2 spot nodes (2 for the mesh session; the default
is 1), Dataplane V2, Cloud Service Mesh on. Sites: `genshin-web` (namespace
`genshin`) and `home` (namespace `home`, added with `sitectl`).

### NetworkPolicies

| | |
| :- | :- |
| How | [deploy/gke/verify-policies.sh](../deploy/gke/verify-policies.sh): probes from ephemeral debug containers inside the site pods |
| Result | 12 of 12 checks pass, with the mesh off and with it on |
| Checked | allowed paths answer (load balancer → site, home → genshin-web, DNS, metadata server, private.googleapis.com); blocked paths don't (genshin-web → home, the Kubernetes API, other Google API addresses, unlisted namespaces) |

Found while testing:

- **NodeLocal DNSCache.** GKE sends pods' DNS to `node-local-dns` pods, not
  kube-dns. The first policy allowed only kube-dns, so every lookup failed;
  the visible symptom was the bucket mount timing out. Fixed in
  [allow-dns.yaml](../deploy/components/network-isolation/allow-dns.yaml).
- **Probes under the mesh.** With a sidecar, Envoy accepts every outbound
  connection locally, so "connected" no longer means "allowed". The probe now
  counts a path as open only on a completed TLS handshake or a real HTTP
  answer.
- An unused egress rule (169.254.169.252:988) was removed.

### Canary releases

| | |
| :- | :- |
| How | [deploy/gke/canary.sh](../deploy/gke/canary.sh) `start <image> 30`, then requests through the load balancer |
| No header, weight 70/30 | 67/33 and 68/32 stable/canary over 100 requests (two runs) |
| `X-Track: stable` | 20 of 20 reached stable |
| `X-Track: canary` | about 97% reached canary (19 of 20 in one run) |
| Rollback, promote | both run; rollback leaves only the base route and stable Deployment |

Found while testing:

- **The first design was rejected by the load balancer.** It matched a
  `Cookie` header with a regular expression. Google's load balancer accepts
  only part of RE2, and only one regex per URL map. The rejected route also
  blocked every other change to the shared Gateway until it was removed.
  Redesigned: the server names its version in an `X-Track` response header,
  the page sends it back for that run, and the route matches it exactly.
- **About 3% leak.** Some requests with `X-Track: canary` reached stable even
  though the URL map was correct, which points to propagation across Google's
  edge. The run page retries (up to 5 times, 300 ms apart) when the answer
  comes from the other version.

### Alerts and dashboard

| | |
| :- | :- |
| How | each PromQL query from [terraform/modules/monitoring](../terraform/modules/monitoring) run against Cloud Monitoring with live traffic |
| Result | every query (5xx share, p95 latency, restarts, dashboard charts) returns data |

### Service mesh

| | |
| :- | :- |
| How | [deploy/gke/verify-mesh.sh](../deploy/gke/verify-mesh.sh) |
| mTLS | home → genshin-web requests show `connection_security_policy="mutual_tls"` from principal `home/home` in the sidecar's metrics; the call returns 200 |
| Identity | an impostor pod in namespace `home` with label `app=home` (so the NetworkPolicy lets it through) but the `default` service account gets `403 RBAC: access denied` |
| Load balancer | plaintext traffic from the load balancer ranges is still allowed |

Found while testing:

- `istioctl` couldn't be downloaded in this environment, so the mesh is
  Google's managed Istio (Cloud Service Mesh), switched on from Terraform.
- With sidecars, genshin-web didn't fit on the node (insufficient CPU). The
  sidecar requests 50m CPU and 64Mi, and genshin-web requests 750m.
- The `home` image first crashed: distroless already has a `/home` folder, so
  the binary now lives at `/site`.

### Teardown

| | |
| :- | :- |
| How | `deploy/gke/down.sh`, switches off, `terraform apply` |
| Result | 0 clusters, VMs, disks, addresses, forwarding rules, NEGs, backend services, URL maps, health checks, certificates, DNS zones and fleet memberships; mesh feature disabled; a fresh `terraform plan` shows no changes; Cloud Run still answers 200 |

Found while testing: deleting the network failed because GKE and the mesh
leave a firewall rule (`gke-csm-thc-*`) and empty NEGs behind.
[deploy/gke/cleanup-orphans.sh](../deploy/gke/cleanup-orphans.sh) deletes
them, and refuses to run while a cluster exists.

### Terraform plan in CI

| | |
| :- | :- |
| How | [plan.yml](../.github/workflows/plan.yml) re-run after the repository secrets were set |
| Result | authenticated through GitHub OIDC and Workload Identity Federation (no key), `No changes. Your infrastructure matches the configuration.` |

### CI

[ci.yml](../.github/workflows/ci.yml) passes: Go tests, `sitectl check`,
kubeconform on every rendered manifest, `terraform fmt` and `validate`. A
Linux-only hang in the simulator timeout test was fixed (`cmd.WaitDelay`).
