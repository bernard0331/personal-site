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
