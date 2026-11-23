# Copy to terraform.tfvars (gitignored) and fill in.
project     = "my-project-id"
runs_bucket = "my-project-id-genshin-runs"

# Optional:
# machine_type        = "e2-standard-2"
# node_count          = 2
# authorized_networks = ["203.0.113.7/32"]

# The GKE copy is off by default; true creates the cluster, network, IP and certificate.
# gke_enabled = true
