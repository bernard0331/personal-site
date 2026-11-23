# The Genshin site's bucket: each run's snapshot, status and result under
# runs/, and a copy of the shared simulation cache. It already exists (made
# for Cloud Run on 2026-11-18); envs/prod imports it.
#
# Uniform bucket-level access: permissions are set on the bucket only, never
# per object (also required for Workload Identity with the FUSE driver).
# Public access prevention: no setting can make it public by mistake.
resource "google_storage_bucket" "runs" {
  name                        = var.name
  location                    = upper(var.region)
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"

  # Saved runs (and their share links) last 90 days, as the site says.
  lifecycle_rule {
    action {
      type = "Delete"
    }
    condition {
      age            = 90
      matches_prefix = ["runs/"]
    }
  }

  # Keep the bucket (and every saved run) if the rest is destroyed: deleting
  # it is a separate, deliberate step.
  lifecycle {
    prevent_destroy = true
  }
}
