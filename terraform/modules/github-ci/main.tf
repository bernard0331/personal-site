# Lets GitHub Actions in this repository run `terraform plan` with no key.
#
# Workload Identity Federation: GitHub issues each workflow run a short-lived
# OIDC token saying which repository and branch it is. Google Cloud trusts
# GitHub's issuer through this pool, accepts tokens only from this
# repository (by its numeric id, which unlike a name can't be re-registered
# by someone else), and exchanges them for short-lived Google credentials.
#
# The repository's principalSet gets read-only roles directly (no Google
# service account to impersonate): enough to refresh state and plan, not to
# change anything. Apply stays on the laptop.
#
# Free: pools, providers and IAM bindings cost nothing.

data "google_project" "this" {}

resource "google_iam_workload_identity_pool" "github" {
  workload_identity_pool_id = "github"
  display_name              = "GitHub Actions"
  description               = "OIDC tokens from GitHub Actions in ${var.repository}"
}

resource "google_iam_workload_identity_pool_provider" "github" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.github.workload_identity_pool_id
  workload_identity_pool_provider_id = "github"
  display_name                       = "GitHub"
  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }
  attribute_mapping = {
    "google.subject"          = "assertion.sub"
    "attribute.repository"    = "assertion.repository"
    "attribute.repository_id" = "assertion.repository_id"
    "attribute.ref"           = "assertion.ref"
    "attribute.event_name"    = "assertion.event_name"
  }
  # Tokens from any other repository are refused at the door.
  attribute_condition = "assertion.repository_id == \"${var.repository_id}\""
}

locals {
  repo_principal = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.github.name}/attribute.repository_id/${var.repository_id}"
}

# Read everything Terraform manages: Viewer covers most resources;
# Security Reviewer adds reading IAM policies (the bucket and project grants).
resource "google_project_iam_member" "plan" {
  for_each = toset(["roles/viewer", "roles/iam.securityReviewer"])
  project  = data.google_project.this.project_id
  role     = each.value
  member   = local.repo_principal
}

# Read Terraform's state (not write: plan runs with -lock=false).
resource "google_storage_bucket_iam_member" "state" {
  bucket = var.state_bucket
  role   = "roles/storage.objectViewer"
  member = local.repo_principal
}
