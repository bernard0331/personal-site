# The Artifact Registry repository for container images. It already exists
# (created by hand for Cloud Run on 2026-11-18); envs/prod imports it, so
# Terraform takes it over without recreating it.
#
# Cleanup policy: images older than 30 days are deleted, except the 10 most
# recent, which are always kept (a KEEP rule wins over a DELETE rule). Each
# image is ~700 MB, and storage beyond 0.5 GB is billed.
resource "google_artifact_registry_repository" "this" {
  repository_id = var.name
  location      = var.region
  format        = "DOCKER"
  description   = var.description

  cleanup_policy_dry_run = false

  cleanup_policies {
    id     = "delete-older-than-30-days"
    action = "DELETE"
    condition {
      tag_state  = "ANY"
      older_than = "2592000s"
    }
  }

  cleanup_policies {
    id     = "keep-10-most-recent"
    action = "KEEP"
    most_recent_versions {
      keep_count = 10
    }
  }
}
