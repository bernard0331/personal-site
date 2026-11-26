# deploy/gke/apply.sh reads these to fill in the manifests.

output "cluster" {
  value = one(module.gke[*].name)
}

output "zone" {
  value = one(module.gke[*].location)
}

output "image_repository" {
  value = module.registry.url
}

output "runs_bucket" {
  value = module.storage.name
}

output "gateway_address_name" {
  value = one(module.dns_certs[*].address_name)
}

output "certificate_map" {
  value = one(module.dns_certs[*].certificate_map)
}

output "hostnames" {
  value = one(module.dns_certs[*].hostnames)
}

output "kubectl_credentials" {
  description = "Run this to point kubectl at the cluster"
  value       = var.gke_enabled ? "gcloud container clusters get-credentials ${module.gke[0].name} --zone ${module.gke[0].location} --project ${var.project}" : "GKE is off (gke_enabled = false)"
}

output "github_workload_identity_provider" {
  description = "Repository secret WIF_PROVIDER for the plan workflow"
  value       = module.github_ci.workload_identity_provider
}

output "fanout_nodes" {
  description = "Most GKE fan-out workers (0: none); deploy/gke/apply.sh reads it"
  value       = var.gke_enabled ? var.fanout_nodes : 0
}
