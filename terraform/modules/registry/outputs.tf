output "url" {
  description = "Image path prefix, e.g. <url>/genshin-web:<tag>"
  value       = "${google_artifact_registry_repository.this.location}-docker.pkg.dev/${google_artifact_registry_repository.this.project}/${google_artifact_registry_repository.this.repository_id}"
}
