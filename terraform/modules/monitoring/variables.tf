variable "namespace" {
  description = "Kubernetes namespace of the site to watch"
  type        = string
}

variable "cluster" {
  description = "GKE cluster name"
  type        = string
}

variable "notification_email" {
  description = "Where alerts are emailed. null: no email; incidents still show in the console"
  type        = string
  default     = null
}

variable "error_ratio" {
  description = "Alert when more than this share of requests get a 5xx (over 10 minutes)"
  type        = number
  default     = 0.05
}

variable "latency_p95_ms" {
  description = "Alert when the 95th percentile request latency stays above this (ms)"
  type        = number
  default     = 2000
}

variable "sites" {
  description = "Sites behind the Gateway that get SLOs (name = Kubernetes Service, namespace)"
  type = list(object({
    name      = string
    namespace = string
  }))
  default = []
}

variable "availability_target" {
  description = "Availability SLO: share of requests that are not 5xx, over 28 days"
  type        = number
  default     = 0.995
}

variable "latency_target" {
  description = "Latency SLO: share of requests answered within latency_threshold_ms, over 28 days"
  type        = number
  default     = 0.95
}

variable "latency_threshold_ms" {
  description = "Latency SLO threshold (ms), measured at the load balancer"
  type        = number
  default     = 500
}

variable "burn_min_requests" {
  description = "A burn-rate alert needs at least this many requests in its long window"
  type        = number
  default     = 50
}
