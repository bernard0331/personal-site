# sitectl

Adds a site to the shared Gateway in one command, so every service is
exposed, routed and isolated the same way without hand-written Kubernetes or
Terraform.

```bash
go run ./tools/sitectl add -name home -site www
```

That one command:

| Where | What it adds |
| :- | :- |
| `terraform/envs/prod/services.json` | The site's entry: name, namespace, hostname label, port, bucket access. This file is the one list of sites; Terraform reads it too. |
| `deploy/base/home/` | Namespace labelled `personal-site/gateway=true` (so its route may attach to the Gateway), Kubernetes service account, Deployment (non-root, read-only root filesystem, probes), Service, the load balancer's health check, HTTPRoute, and the default-deny NetworkPolicies (`deploy/components/network-isolation`). |
| `deploy/overlays/prod/kustomization.yaml` | The site's manifests and image, regenerated from `services.json`. |

From the same entry, `terraform apply` then creates:

- the hostname `www.<gateway ip>.sslip.io`;
- the site's own TLS certificate and certificate map entry;
- a firewall opening for its port to the load balancer;
- with `-bucket`, Workload Identity access for its service account to its own folder of the runs bucket.

`deploy/gke/apply.sh` fills in each site's hostname and deploys everything.

Other commands:

- `sitectl list` shows the sites.
- `sitectl render` rewrites the overlay after `services.json` is edited by hand.
- `sitectl check` changes nothing. It fails if a site has no manifests, lacks the network policies, isn't labelled for the Gateway, has the wrong hostname placeholder, or if the overlay is out of date. It's meant for CI.

Names are checked before anything is written. They must be DNS labels, unique per name, namespace and site, and must not be a reserved namespace. Ports must be 1024 or above, because pods run as non-root. Nothing is written if any check fails.
