# Bootstrap: what must exist before the main configuration (envs/prod) can
# run. Applied once, by hand, with local state.

# The APIs the main configuration uses. Turning an API on costs nothing by
# itself; the resources created through it do. disable_on_destroy = false so
# that destroying this folder never switches off an API something else uses
# (Cloud Run, the registry and the bucket already rely on several).
locals {
  services = [
    "artifactregistry.googleapis.com",   # container images
    "certificatemanager.googleapis.com", # the Gateway's TLS certificate
    "compute.googleapis.com",            # VPC, subnet, static IP, load balancer
    "container.googleapis.com",          # GKE
    "dns.googleapis.com",                # private googleapis.com zone
    "iam.googleapis.com",                # service accounts
    "iamcredentials.googleapis.com",     # Workload Identity token exchange
    "monitoring.googleapis.com",         # alert policies and the dashboard
    "storage.googleapis.com",            # buckets
  ]
}

resource "google_project_service" "this" {
  for_each           = toset(local.services)
  service            = each.value
  disable_on_destroy = false
}

# Terraform state for envs/prod. Versioning keeps old copies of the state, so
# a bad apply or a corrupted file can be rolled back; old versions are
# deleted after 30 days to keep the bucket tiny.
resource "google_storage_bucket" "state" {
  name                        = "${var.project}-tfstate"
  location                    = upper(var.region)
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = false

  versioning {
    enabled = true
  }

  lifecycle_rule {
    action {
      type = "Delete"
    }
    condition {
      days_since_noncurrent_time = 30
      with_state                 = "ARCHIVED"
    }
  }
}
