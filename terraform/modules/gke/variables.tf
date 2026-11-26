variable "project" {
  type = string
}

variable "name" {
  description = "Cluster name"
  type        = string
}

variable "zone" {
  description = "Zone for the zonal cluster and its nodes"
  type        = string
}

variable "network" {
  type = string
}

variable "subnetwork" {
  type = string
}

variable "pods_range" {
  type = string
}

variable "services_range" {
  type = string
}

variable "machine_type" {
  description = "Node machine type"
  type        = string
}

variable "node_count" {
  description = "Nodes in the spot pool (0 stops every workload but keeps the cluster)"
  type        = number
}

variable "disk_size_gb" {
  type    = number
  default = 30
}

variable "authorized_networks" {
  description = "CIDRs allowed to reach the control plane; empty allows any address (IAM still applies)"
  type        = list(string)
  default     = []
}

variable "deletion_protection" {
  description = "Refuse terraform destroy of the cluster while true"
  type        = bool
  default     = false
}

variable "mesh" {
  description = "Register the cluster in the fleet for Cloud Service Mesh"
  type        = bool
  default     = false
}

variable "worker_nodes" {
  description = "Most fan-out worker nodes (spot, autoscaled from zero); 0: no worker pool"
  type        = number
  default     = 0
}

variable "worker_machine_type" {
  description = "Fan-out worker machine: 8 vCPU, like a Cloud Run worker"
  type        = string
  default     = "c2d-highcpu-8"
}
