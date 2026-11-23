output "name" {
  value = google_container_cluster.this.name
}

output "location" {
  value = google_container_cluster.this.location
}

output "workload_pool" {
  description = "Workload Identity pool, used to grant Kubernetes service accounts Google Cloud roles"
  value       = google_container_cluster.this.workload_identity_config[0].workload_pool
}
