variable "name" {
  description = "Name of the VPC; the subnet is named <name>-<region>"
  type        = string
}

variable "region" {
  type = string
}

variable "nodes_cidr" {
  description = "Primary range: the nodes"
  type        = string
  default     = "10.10.0.0/24"
}

variable "pods_cidr" {
  description = "Secondary range for pods (a /20 is ~4,000 pod addresses, plenty for a few nodes)"
  type        = string
  default     = "10.20.0.0/20"
}

variable "services_cidr" {
  description = "Secondary range for ClusterIP Services"
  type        = string
  default     = "10.30.0.0/24"
}

variable "app_port" {
  description = "Port the pods serve on, opened to the load balancer"
  type        = number
  default     = 8080
}
