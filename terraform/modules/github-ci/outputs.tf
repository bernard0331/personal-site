output "workload_identity_provider" {
  description = "For google-github-actions/auth's workload_identity_provider"
  value       = google_iam_workload_identity_pool_provider.github.name
}
