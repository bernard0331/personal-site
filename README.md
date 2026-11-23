# personal-site

Three websites, planned to share one Google Kubernetes Engine (GKE) cluster:

- **A landing page** about me, linking to the other two. Not started.
- **A Genshin Impact team optimizer**: give it your account and it finds your
  strongest teams with no character or weapon used twice. Live for friends to
  test. See [services/genshin-calc](services/genshin-calc) (simulation and
  team selection) and [services/genshin-web](services/genshin-web) (the site).
- **A Terraforming Mars site** for games played through
  [tfm-cli](https://github.com/bernard0331/tfm-cli). Not started.

> For a single small site, Cloud Run is simpler and cheaper than GKE, and
> that is what serves the optimizer today. The GKE version exists to practise
> Kubernetes, Kubernetes networking, Terraform, Go and GitHub Actions.

## Status

| Part | State |
| :- | :- |
| Optimizer core (simulator, rotations, team selection) | Done; runs locally and in the site |
| Optimizer site | Live on Cloud Run, one instance at most, none while idle. See [deploy/genshin-web](deploy/genshin-web/README.md) |
| Terraform | Written and applied. Manages the registry, bucket and service accounts, and the GKE version behind a switch |
| GKE version | Defined in code and tested (2026-11-23), then switched off so it costs nothing. See [terraform](terraform/README.md) and [deploy/gke](deploy/gke/README.md) |
| Argo CD, GitHub Actions, network policies, IAP | Not done |
| Landing page, Terraforming Mars site | Not started |

## Layout

| Folder | What it holds |
| :- | :- |
| `services/genshin-calc` | The optimizer: runs KQM Sim (a patched build), picks weapons, solves team selection exactly. AGPL-3.0 |
| `services/genshin-web` | The optimizer's website and JSON API (Go standard library). AGPL-3.0 |
| `deploy/genshin-web` | Building the image and deploying it to Cloud Run |
| `deploy/base`, `deploy/overlays`, `deploy/gke` | Kubernetes manifests (Kustomize) for the GKE version, and the script that applies them |
| `terraform/bootstrap` | APIs and the bucket that holds Terraform state (local state, run once) |
| `terraform/modules`, `terraform/envs/prod` | Network, GKE cluster, registry, storage, IAM, IP address and certificates |
| `local/` | Gitignored: accounts, caches, game images, notes |

## Running the site

Locally: see [services/genshin-web](services/genshin-web/README.md).

On Cloud Run (the live site):

```bash
deploy/genshin-web/build.sh
```

```bash
BUCKET=<the bucket> deploy/genshin-web/deploy.sh <the image build.sh printed>
```

On GKE: set `gke_enabled = true` in `terraform/envs/prod/terraform.tfvars`,
run `terraform apply` there, then `deploy/gke/apply.sh`. One spot node costs
about $30 a month and the load balancer about $18, so switch it off again
afterwards (steps in [terraform/README.md](terraform/README.md)). It keeps its
data in its own folder of the bucket, so it can run alongside Cloud Run.

## Local tools

| Tool | Used for |
| :- | :- |
| Go | All services |
| Python with HiGHS | Team selection (exact solver) |
| Docker and kind | Local Kubernetes cluster |
| kubectl | Talking to the local and GKE clusters |
| Terraform | Cloud resources |
| gcloud | Google Cloud CLI, credentials and Cloud Build |

Create and delete the local cluster:

```bash
kind create cluster --name personal-site
kind delete cluster --name personal-site
```

## Rules for this repository

- No project IDs, emails or secrets. Use variables and a gitignored tfvars file.
- No service account keys. Use Workload Identity, GitHub OIDC and application
  default credentials.
- HoYoverse's images and the KQM Sim Database cache are never committed.
