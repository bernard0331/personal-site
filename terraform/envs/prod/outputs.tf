# deploy/gke/apply.sh reads these to fill in the manifests.

output "cluster" {
  value = module.gke.name
}

output "zone" {
  value = module.gke.location
}

output "image_repository" {
  value = module.registry.url
}

output "runs_bucket" {
  value = module.storage.name
}

output "gateway_address_name" {
  value = module.dns_certs.address_name
}

output "certificate_map" {
  value = module.dns_certs.certificate_map
}

output "hostnames" {
  value = module.dns_certs.hostnames
}

output "kubectl_credentials" {
  description = "Run this to point kubectl at the cluster"
  value       = "gcloud container clusters get-credentials ${module.gke.name} --zone ${module.gke.location} --project ${var.project}"
}
