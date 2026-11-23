# Values come from terraform.tfvars (gitignored); example.tfvars shows the
# shape.

variable "project" {
  description = "GCP project id"
  type        = string
}

variable "region" {
  type    = string
  default = "us-central1"
}

variable "zone" {
  description = "Zone for the cluster and its nodes (design doc: any US zone)"
  type        = string
  default     = "us-central1-a"
}

variable "gke_enabled" {
  description = "Create the GKE copy of the site (cluster, network, Gateway IP, certificate). Off: only the resources Cloud Run uses remain, and GKE costs nothing"
  type        = bool
  default     = false
}

variable "runs_bucket" {
  description = "The Genshin site's existing bucket (runs and the cache's copy)"
  type        = string
}

variable "machine_type" {
  description = "Spot node machine type. e2-standard-2 = 2 vCPU, 8 GB"
  type        = string
  default     = "e2-standard-2"
}

variable "node_count" {
  description = "Spot nodes. One keeps the cost down (two would keep the site up when Google takes one back); 0 parks the cluster"
  type        = number
  default     = 1
}

variable "authorized_networks" {
  description = "CIDRs allowed to reach the cluster's control plane (e.g. your home IP/32); empty = any (IAM still applies)"
  type        = list(string)
  default     = []
}
