# Resources that already exist, made by hand for the Cloud Run version on
# 2026-11-18. An import block tells Terraform to adopt the existing resource
# into its state on the next apply instead of creating a new one; the plan
# shows "will be imported" and any setting Terraform would change.
# Once applied, these blocks can stay (they do nothing) or be deleted.

import {
  to = module.registry.google_artifact_registry_repository.this
  id = "projects/${var.project}/locations/${var.region}/repositories/genshin"
}

import {
  to = module.storage.google_storage_bucket.runs
  id = var.runs_bucket
}

import {
  to = module.iam.google_service_account.web
  id = "projects/${var.project}/serviceAccounts/genshin-web@${var.project}.iam.gserviceaccount.com"
}

import {
  to = module.iam.google_storage_bucket_iam_member.web
  id = "b/${var.runs_bucket} roles/storage.objectUser serviceAccount:genshin-web@${var.project}.iam.gserviceaccount.com"
}
