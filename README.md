# personal-site

A personal website hosted on Google Kubernetes Engine (GKE). Each section of the
site is its own set of Go services behind one gateway:

- **Genshin Impact**: damage calculator, team comparison and planning.
- **Terraforming Mars**: game history and statistics from games played through
  [tfm-cli](https://github.com/bernard0331/tfm-cli).

Cloud resources are managed with Terraform, and what runs in the cluster is
deployed by Argo CD from this repository.

> For a single small site, Cloud Run would be simpler and cheaper than GKE.
> This project uses GKE on purpose, to practise Kubernetes, Kubernetes
> networking, Terraform, Go and GitHub Actions.

## Status

Phase 0 (setup) is done. Next is phase 1: the calculator core.

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
