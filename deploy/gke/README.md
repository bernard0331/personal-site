# deploy/gke

`apply.sh` deploys `deploy/overlays/prod` to the GKE cluster that
`terraform/envs/prod` creates. See `terraform/README.md` for bringing the
cluster up, cost, running it alongside Cloud Run and taking it down.

| Manifest | What it is |
| :- | :- |
| `base/gateway/` | The `gateway` namespace, the shared Gateway (global external Application Load Balancer on the reserved IP, TLS from the certificate map) and the HTTP to HTTPS redirect. |
| `base/genshin-web/` | The `genshin` namespace, the pod's service account (Workload Identity), the Deployment (one replica, the runs bucket mounted at /data), its Service, the load balancer's health check and the HTTPRoute for the site's hostname. |
| `overlays/prod/` | Both bases and the image. Placeholders that would contain the project id are filled in by `apply.sh` from Terraform's outputs. |
