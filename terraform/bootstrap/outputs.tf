output "state_bucket" {
  description = "Pass to envs/prod: terraform init -backend-config=\"bucket=<this>\""
  value       = google_storage_bucket.state.name
}
