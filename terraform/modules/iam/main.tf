# Who may use the runs bucket. Least privilege: object read/write on this one
# bucket (roles/storage.objectUser), nothing project-wide.
#
# Two identities:
# - The Google service account genshin-web, which the Cloud Run service runs
#   as. It already exists; envs/prod imports it.
# - The Kubernetes service account genshin/genshin-web on GKE. With Workload
#   Identity Federation for GKE it's granted the role directly, as a
#   principal:// member, so no Google service account or key is involved.

data "google_project" "this" {}

resource "google_service_account" "web" {
  account_id   = "genshin-web"
  display_name = "genshin-web (Cloud Run)"
}

locals {
  # principal://iam.googleapis.com/projects/NUMBER/locations/global/workloadIdentityPools/POOL/subject/ns/NAMESPACE/sa/NAME
  ksa_members = [
    for ksa in var.kubernetes_service_accounts :
    "principal://iam.googleapis.com/projects/${data.google_project.this.number}/locations/global/workloadIdentityPools/${var.workload_pool}/subject/ns/${ksa.namespace}/sa/${ksa.name}"
  ]
}

resource "google_storage_bucket_iam_member" "web" {
  bucket = var.bucket
  role   = "roles/storage.objectUser"
  member = google_service_account.web.member
}

resource "google_storage_bucket_iam_member" "ksa" {
  for_each = toset(local.ksa_members)
  bucket   = var.bucket
  role     = "roles/storage.objectUser"
  member   = each.value
}
