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

output "fleet_membership" {
  description = "Fleet membership id and location (null without the mesh)"
  value = var.mesh ? {
    id       = google_container_cluster.this.fleet[0].membership_id
    location = google_container_cluster.this.fleet[0].membership_location
  } : null
}
