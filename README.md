# personal-site

Three websites on one Google Kubernetes Engine (GKE) cluster:

- **A landing page** about me, linking to the other two.
- **A Genshin Impact team optimizer**: give it your account and it finds your
  strongest teams with no character or weapon used twice. See
  [services/genshin-calc](services/genshin-calc).
- **A Terraforming Mars site** for games played through
  [tfm-cli](https://github.com/bernard0331/tfm-cli).

Cloud resources are managed with Terraform, and what runs in the cluster is
deployed by Argo CD from this repository.

> For a single small site, Cloud Run would be simpler and cheaper than GKE.
> This project uses GKE on purpose, to practise Kubernetes, Kubernetes
> networking, Terraform, Go and GitHub Actions.

## Status

Phase 0 (setup) is done. Phase 1, the optimizer core, is in progress: the
simulator builds and runs locally.

## Local tools

| Tool | Used for |
| :- | :- |
| Go | All services |
| Docker and kind | Local Kubernetes cluster |
| kubectl | Talking to the local and GKE clusters |
| Terraform | Cloud resources |
| gcloud | Google Cloud CLI and credentials |

Create and delete the local cluster:

```bash
kind create cluster --name personal-site
kind delete cluster --name personal-site
```

## Rules for this repository

- No project IDs, emails or secrets. Use variables and a gitignored tfvars file.
- No service account keys. Use Workload Identity, GitHub OIDC and application
  default credentials.
