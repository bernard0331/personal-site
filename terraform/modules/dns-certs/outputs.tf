output "address_name" {
  description = "For the Gateway's spec.addresses (type NamedAddress)"
  value       = google_compute_global_address.gateway.name
}

output "address" {
  value = google_compute_global_address.gateway.address
}

output "certificate_map" {
  description = "For the Gateway's networking.gke.io/certmap annotation"
  value       = google_certificate_manager_certificate_map.this.name
}

output "hostnames" {
  value = local.hostnames
}
