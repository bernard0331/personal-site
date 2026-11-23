output "network" {
  value = google_compute_network.this.id
}

output "subnetwork" {
  value = google_compute_subnetwork.this.id
}

output "pods_range" {
  value = "pods"
}

output "services_range" {
  value = "services"
}
