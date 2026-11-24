# Terraform: the GKE version of the site

**Status:** see `envs/prod/switches.auto.tfvars`. The GKE copy is fully
defined here and is switched on only while being worked on. Off, it costs
nothing: only what the Cloud Run site uses exists (bucket, image repository,
its service account), plus Terraform's state bucket and the GitHub OIDC
pool, which are free.

Cloud resources for running the sites on Google Kubernetes Engine (design doc
phases 3 and 4). Terraform owns the cloud resources; what runs inside the
cluster is in `deploy/` as Kubernetes manifests.

The Cloud Run service (`deploy/genshin-web/`) keeps running alongside; see
"Alongside Cloud Run" below.

## Layout

| Folder | What it makes |
| :- | :- |
| `bootstrap/` | Turns on the APIs and makes the bucket that holds Terraform's state. Applied once, state kept on the laptop. |
| `modules/network` | Custom VPC, one subnet with pod and Service ranges, Private Google Access, a firewall rule for the load balancer on the sites' ports. No Cloud NAT. A private DNS zone points `*.googleapis.com` at private.googleapis.com (199.36.153.8/30), so the pods' egress NetworkPolicy can allow exactly those four addresses. |
| `modules/gke` | Zonal Standard cluster (Dataplane V2, Workload Identity, Gateway API, Cloud Storage FUSE driver, private nodes) and one spot node pool with its own least-privilege service account. |
| `modules/registry` | The `genshin` Artifact Registry repository with a cleanup policy (keep the 10 newest images, delete others after 30 days). |
| `modules/storage` | The runs bucket (90-day rule on `runs/`, never destroyed by Terraform). |
| `modules/iam` | Object read/write on that bucket for Cloud Run's service account and for the pod's Kubernetes service account (Workload Identity Federation, no keys). |
| `modules/dns-certs` | Reserved global IP, an sslip.io hostname per site, a Google-managed certificate per site (load balancer authorization) and the certificate map the Gateway uses. |
| `modules/monitoring` | Cloud Monitoring alert policies in PromQL (5xx share and p95 latency at the load balancer, container restarts) and a dashboard. Email goes to `alert_email` in `terraform.tfvars` if it's set. |
| `modules/mesh` | Cloud Service Mesh (managed Istio): the fleet's mesh feature and this cluster's membership in it, with Google managing the control plane and the sidecars. |
| `modules/github-ci` | Workload Identity Federation for GitHub Actions: a pool and provider that accept only this repository's OIDC tokens, with read-only roles, for `terraform plan` in CI and no keys. |
| `envs/prod` | Wires the modules together. `switches.auto.tfvars` (committed) holds `gke_enabled` (network, gke, dns-certs, monitoring), `mesh_enabled` (mesh) and `node_count`. `services.json` lists the sites (managed by `tools/sitectl`); each one gets a hostname, a certificate, a firewall port and, if it asks, bucket access. `imports.tf` adopts the registry, bucket and service account made by hand for Cloud Run, so they aren't created twice. |

Values that contain the project id live in `envs/prod/terraform.tfvars` and
`bootstrap/terraform.tfvars` (gitignored; copy `example.tfvars`). The on/off
switches are not secret and live in the committed `switches.auto.tfvars`, so
CI plans against the same state and switching something on or off is a
commit.

## Cost (checked 2026-11-23)

Prices from the Cloud Billing Catalog API, us-central1:

| Item | Price | Per month |
| :- | :- | :- |
| Spot e2-standard-2 node (2 vCPU x $0.01309/h + 8 GB x $0.00175/h) | $0.0402/h | ~$29 per node |
| Standard persistent disk, 30 GB per node | $0.04/GB-month | ~$1.20 per node |
| Cluster management fee (one zonal cluster) | covered by the GKE free tier credit | $0 |
| Gateway load balancer (forwarding rule minimum, global) | $0.025/h | ~$18 |
| Private DNS zone for googleapis.com | $0.20/zone-month | $0.20 |
| Alert policies and dashboard (first million alert time series free) | $0 | $0 |
| Cloud Service Mesh, per cluster in the mesh (`mesh_enabled`) | ~$0.07/h | ~$50 |
| GitHub OIDC pool and its read-only grants | $0 | $0 |

A working session with everything on (2 spot nodes, load balancer, mesh)
costs about $0.25 an hour.

The design doc's two nodes would cost about $60 a month before the load
balancer, more than the $50 budget, so the owner runs one (`node_count = 1`
in `switches.auto.tfvars`, chosen 2026-11-23; 2 while testing the mesh and
canaries): about $30. A spot reclaim takes
the GKE copy down until GKE replaces the node (minutes); unfinished runs
resume. `node_count = 0` parks the cluster. Spot prices change; check before
applying.

## Bringing it up

Each `apply` shows its plan and waits for "yes". Read it first. The
bootstrap folder is already applied; on a fresh project, apply it first:

```bash
cd terraform/bootstrap && terraform init && terraform apply
```

Then set `gke_enabled = true` (and `mesh_enabled = true` for the mesh) in
`envs/prod/switches.auto.tfvars` and:

```bash
cd terraform/envs/prod && terraform init -backend-config="bucket=$(terraform -chdir=../../bootstrap output -raw state_bucket)" && terraform apply
```

The cluster takes about 10 minutes. Then point kubectl at it and deploy an
image that `deploy/genshin-web/build.sh` built:

```bash
eval "$(terraform -chdir=terraform/envs/prod output -raw kubectl_credentials)"
```

```bash
deploy/gke/apply.sh <the image build.sh printed>
```

`apply.sh` deploys every site at the image's tag (build `home` with
`deploy/home/build.sh` at the same commit, or tag its image), and the Istio
policies in `deploy/mesh` when the mesh is installed. The managed mesh takes
about 10 minutes after `apply` to install; pods started before then have no
sidecar until restarted (`kubectl -n genshin rollout restart deploy/genshin-web`,
and the same for `home`).

Check the network policies from inside the pods:

```bash
deploy/gke/verify-policies.sh <the genshin-web image>
```

The script prints the site's address. The certificate is issued once the
Gateway answers for the hostname, usually within an hour; until then HTTPS
fails. Check it with:

```bash
gcloud certificate-manager certificates list --format='table(name,managed.state)'
```

## Alongside Cloud Run

The Cloud Run service stays the main site; the GKE copy is for learning.
They share the runs bucket but never each other's files: the GKE pod mounts
only the bucket's `gke/` folder (the `only-dir` mount option), so its runs,
share links and cache are separate (`gke/runs/`, `gke/cache.jsonl`). Both can
run at once, and either can be taken down without touching the other.
(`gke/cache.jsonl` was seeded with a copy of Cloud Run's cache on
2026-11-23; the simulator binary is the same, so it stays valid.)

To stop paying for the node while keeping the cluster, set `node_count = 0`
and apply; set it back to 1 to bring the site back. The load balancer keeps
billing until the Gateway is deleted (see below).

Simulations on GKE run 2 at a time (one 2-vCPU node) instead of Cloud Run's
7, so runs take roughly three times as long; `machine_type` and the
Deployment's `-cpus` change that.

## Taking it down

Delete what runs in the cluster first: the Gateway's load balancer was made
by GKE, not Terraform, and would be left behind (and keep billing).
`down.sh` stops any canary, deletes the sites, the mesh policies and the
Gateway, and waits until no forwarding rule or endpoint group is left:

```bash
deploy/gke/down.sh
```

Then set `gke_enabled = false` and `mesh_enabled = false` in
`envs/prod/switches.auto.tfvars`, apply, and commit the file:

```bash
terraform -chdir=terraform/envs/prod apply
```

If deleting the network fails because something is "already being used",
GKE or the mesh left resources outside Terraform (empty endpoint groups, the
mesh's firewall rule). Remove them and apply again:

```bash
deploy/gke/cleanup-orphans.sh
```

That destroys the cluster (and with it its fleet membership and the mesh),
nodes, network, DNS zone, IP address, certificates, alerts and dashboard,
and nothing else. Check with `gcloud compute forwarding-rules
list` that no load balancer is left. Remaining cost: cents a month for the
buckets and images, which Cloud Run uses anyway.

Parking instead (`node_count = 0`, Gateway kept) costs about $18 a month for
the load balancer, so switching off is cheaper; bringing it back is about 15
minutes plus up to an hour for a new certificate (the IP, and so the
hostname, will be new).

## Not done yet

- Secret Manager and the admin hostname behind IAP (design doc phase 5; the
  site has no admin pages yet). Network policies are in
  `deploy/components/network-isolation`.
- Argo CD (phase 7); `deploy/gke/apply.sh` stands in. GitHub Actions runs
  tests, validation and `terraform plan` (see "Plan in CI").
- A PodDisruptionBudget: with one replica, one that keeps a pod up would
  block node upgrades. It comes with the API and worker split.

## Plan in CI

`.github/workflows/plan.yml` runs `terraform plan` on every push to main and
every pull request from this repository. It has no key: GitHub's OIDC token
is exchanged for short-lived Google credentials through `modules/github-ci`,
which accepts only this repository and grants only read roles. It needs four
repository secrets (secrets, so GitHub masks them in the public logs), set
once from this folder:

```bash
gh secret set GCP_PROJECT --body "$(gcloud config get-value project)"
```

```bash
gh secret set GCP_PROJECT_NUMBER --body "$(gcloud projects describe "$(gcloud config get-value project)" --format='value(projectNumber)')"
```

```bash
gh secret set RUNS_BUCKET --body "$(terraform -chdir=terraform/envs/prod output -raw runs_bucket)"
```

```bash
gh secret set WIF_PROVIDER --body "$(terraform -chdir=terraform/envs/prod output -raw github_workload_identity_provider)"
```

They are set (2026-11-23), so the workflow plans for real. If they were
removed, it would skip with a notice instead of failing.
