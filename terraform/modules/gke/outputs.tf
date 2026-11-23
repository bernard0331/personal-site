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
  # A cluster registered through its fleet block gets a membership named
  # after the cluster, in the cluster's region. Built here rather than read
  # from the fleet block, which is only known after the cluster is updated;
  # referring to the cluster still orders the mesh after its registration.
  value = var.mesh ? {
    id       = google_container_cluster.this.name
    location = join("-", slice(split("-", google_container_cluster.this.location), 0, 2))
  } : null
}
