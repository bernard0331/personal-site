# personal-site

A Genshin Impact team optimizer, written in Go, with the infrastructure to
run it on Google Kubernetes Engine (GKE). Everything in Google Cloud is
created by Terraform, and what runs in the cluster is described by Kustomize
manifests.

The optimizer takes a player's account and finds their strongest teams with
no character or weapon used twice. It simulates each candidate team with a
patched build of [KQM Sim](https://github.com/KQM-git/kqmsim) and then
solves the team selection exactly. See
[services/genshin-calc](services/genshin-calc) for the optimizer and
[services/genshin-web](services/genshin-web) for the site.

> The GKE environment is brought up from Terraform when I work on it and
> torn down to zero cost between sessions (one `gke_enabled` switch). Between
> sessions, friends use the same container image on Cloud Run, which scales
> to zero.

## Architecture

```
visitor ──HTTPS──▶ global external Application Load Balancer
                   (Gateway API, Certificate Manager TLS, reserved IP)
                        │ container-native load balancing (NEG)
                        ▼
   VPC "personal-site" ─ subnet: nodes / pods / services ranges
   ┌──────────────────────────────────────────────────────────┐
   │ GKE Standard, zonal, private nodes, Dataplane V2         │
   │   namespace genshin                                      │
   │     genshin-web pod ── GCS FUSE ──▶ runs bucket (gke/)   │
   │       identity: Workload Identity Federation (no keys)   │
   └──────────────────────────────────────────────────────────┘
        │ Private Google Access (no Cloud NAT, no internet egress)
        ▼
   Artifact Registry · Cloud Storage · Logging · Monitoring
```

### Networking

- **VPC-native cluster** on a custom-mode VPC: one subnet with secondary
  ranges for pods and Services, so every pod gets a routable VPC address.
- **Private nodes**: nodes have internal addresses only. There is no Cloud
  NAT, so pods cannot reach the internet. Private Google Access lets the nodes
  reach Google APIs (pulling images, the bucket, logs and metrics).
- **Firewall**: an explicit rule admits only Google's load balancer and
  health-check ranges (35.191.0.0/16, 130.211.0.0/22) on the app's port.
- **Dataplane V2**: GKE's eBPF dataplane, based on Cilium, which also
  enforces Kubernetes NetworkPolicies.
- **Gateway API**: one shared `Gateway` (class
  `gke-l7-global-external-managed`) on a reserved global IP. Each site's
  `HTTPRoute` attaches to its HTTPS listener from its own namespace, allowed
  by a namespace label (`personal-site/gateway=true`). Plain HTTP only
  redirects to HTTPS.
- **TLS**: a Google-managed Certificate Manager certificate with load
  balancer authorization, served through a certificate map. Hostnames use
  sslip.io until a domain is bought.
- **Client IP**: behind the load balancer the visitor's address is read from
  a fixed position in `X-Forwarded-For` (`-proxy-hops`), so a forged header
  cannot bypass the per-visitor rate limits.

### Identity and storage

- **Workload Identity Federation for GKE**: the pod's Kubernetes service
  account is granted object access on the one bucket directly, as a
  `principal://` member. No Google service account keys exist anywhere.
- **Least-privilege nodes**: the nodes run as their own service account,
  which may only write logs and metrics and pull images (not the default
  Compute Engine account with Editor).
- **Cloud Storage FUSE**: the runs bucket is mounted into the pod with the
  GKE CSI driver, limited to its own folder so the GKE and Cloud Run copies
  never share data.

### Compute

- One zonal GKE Standard cluster (the free tier covers its management fee)
  with a spot node pool, shielded nodes with secure boot, and a weekend
  maintenance window.
- The pod runs as a non-root user with all capabilities dropped and the
  default seccomp profile, with startup, readiness and liveness probes.

## Repository layout

| Path | What it holds |
| :- | :- |
| `services/genshin-calc` | The optimizer: simulation, weapon choice, exact team selection (AGPL-3.0) |
| `services/genshin-web` | The site and its JSON API, Go standard library only (AGPL-3.0) |
| `terraform/bootstrap` | Enables APIs, creates the versioned Terraform state bucket |
| `terraform/modules/network` | VPC, subnet with pod and Service ranges, load balancer firewall rule |
| `terraform/modules/gke` | Cluster, spot node pool, node service account |
| `terraform/modules/iam` | Bucket access for the Cloud Run and Kubernetes identities |
| `terraform/modules/registry` | Artifact Registry repository with cleanup policies |
| `terraform/modules/storage` | Runs bucket with lifecycle rules |
| `terraform/modules/dns-certs` | Reserved IP, hostnames, certificate and certificate map |
| `terraform/envs/prod` | The environment that wires the modules together (GCS backend) |
| `deploy/base`, `deploy/overlays/prod` | Kustomize manifests: Gateway, namespace, Deployment, Service, HTTPRoute, health check |
| `deploy/gke/apply.sh` | Renders the overlay with Terraform's outputs and applies it |
| `deploy/genshin-web` | Image build (Cloud Build) and the Cloud Run deployment |

## Bringing it up and down

Set `gke_enabled = true` in the gitignored `terraform/envs/prod/terraform.tfvars`, then:

```bash
terraform -chdir=terraform/envs/prod apply
```

```bash
deploy/gke/apply.sh <image>
```

Taking it down deletes the Kubernetes resources first (so the load balancer
goes), then applies with `gke_enabled=false`. The full steps and the cost
are in [terraform/README.md](terraform/README.md) and
[deploy/gke/README.md](deploy/gke/README.md).

## Roadmap (planned, not built yet)

- A landing page and a Terraforming Mars site, as further routes on the same
  Gateway.
- CI: `terraform plan` and manifest validation in GitHub Actions, using
  GitHub OIDC (no keys).
- GitOps deploys with Argo CD in place of `apply.sh`.
- Identity-Aware Proxy for admin pages.

## Rules for this repository

- No project IDs, emails or secrets. Use variables and a gitignored tfvars
  file.
- No service account keys. Use Workload Identity, GitHub OIDC and application
  default credentials.
- HoYoverse's images and the KQM Sim Database cache are never committed.
