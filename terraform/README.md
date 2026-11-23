# Terraform: the GKE version of the site

Cloud resources for running the sites on Google Kubernetes Engine (design doc
phases 3 and 4). Terraform owns the cloud resources; what runs inside the
cluster is in `deploy/` as Kubernetes manifests.

The Cloud Run service (`deploy/genshin-web/`) keeps running alongside; see
"Alongside Cloud Run" below.

## Layout

| Folder | What it makes |
| :- | :- |
| `bootstrap/` | Turns on the APIs and makes the bucket that holds Terraform's state. Applied once, state kept on the laptop. |
| `modules/network` | Custom VPC, one subnet with pod and Service ranges, Private Google Access, a firewall rule for the load balancer. No Cloud NAT. |
| `modules/gke` | Zonal Standard cluster (Dataplane V2, Workload Identity, Gateway API, Cloud Storage FUSE driver, private nodes) and one spot node pool with its own least-privilege service account. |
| `modules/registry` | The `genshin` Artifact Registry repository with a cleanup policy (keep the 10 newest images, delete others after 30 days). |
| `modules/storage` | The runs bucket (90-day rule on `runs/`, never destroyed by Terraform). |
| `modules/iam` | Object read/write on that bucket for Cloud Run's service account and for the pod's Kubernetes service account (Workload Identity Federation, no keys). |
| `modules/dns-certs` | Reserved global IP, an sslip.io hostname per site, a Google-managed certificate (load balancer authorization) and the certificate map the Gateway uses. |
| `envs/prod` | Wires the modules together. `imports.tf` adopts the registry, bucket and service account made by hand for Cloud Run, so they aren't created twice. |

Values that contain the project id live in `envs/prod/terraform.tfvars` and
`bootstrap/terraform.tfvars` (gitignored; copy `example.tfvars`).

## Cost (checked 2026-11-23)

Prices from the Cloud Billing Catalog API, us-central1:

| Item | Price | Per month |
| :- | :- | :- |
| Spot e2-standard-2 node (2 vCPU x $0.01309/h + 8 GB x $0.00175/h) | $0.0402/h | ~$29 per node |
| Standard persistent disk, 30 GB per node | $0.04/GB-month | ~$1.20 per node |
| Cluster management fee (one zonal cluster) | covered by the GKE free tier credit | $0 |
| Gateway load balancer (forwarding rule + data processed) | not retrieved; check the pricing calculator | roughly $18+ |

The design doc's two nodes would cost about $60 a month before the load
balancer, more than the $50 budget, so the owner runs one (`node_count = 1`
in `terraform.tfvars`, chosen 2026-11-23): about $30. A spot reclaim takes
the GKE copy down until GKE replaces the node (minutes); unfinished runs
resume. `node_count = 0` parks the cluster. Spot prices change; check before
applying.

## Bringing it up

Each `apply` shows its plan and waits for "yes". Read it first.

```bash
cd terraform/bootstrap && terraform init && terraform apply
```

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

The script prints the site's address. The certificate is issued once the
Gateway answers for the hostname, usually within an hour; until then HTTPS
fails. Check it with:

```bash
gcloud certificate-manager certificates describe sites --format='yaml(managed)'
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

```bash
kubectl delete -k deploy/overlays/prod
```

```bash
terraform -chdir=terraform/envs/prod destroy -target=module.gke -target=module.network -target=module.dns_certs
```

Delete the Gateway (the first command) before destroying the network: the
load balancer GKE made for it isn't in Terraform's state. The bucket, the
registry and Cloud Run's service account are left: Cloud Run uses them, and
the bucket refuses to be destroyed (`prevent_destroy`) so saved runs survive.

## Not done yet

- Network policies, Secret Manager and the admin hostname behind IAP
  (design doc phase 5; the site has no admin pages yet).
- Argo CD and GitHub Actions (phase 7); `deploy/gke/apply.sh` stands in.
- A PodDisruptionBudget: with one replica, one that keeps a pod up would
  block node upgrades. It comes with the API and worker split.
