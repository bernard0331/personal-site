variable "bucket" {
  description = "Bucket the site reads and writes"
  type        = string
}

variable "workload_pool" {
  description = "The cluster's Workload Identity pool (PROJECT.svc.id.goog)"
  type        = string
}

variable "kubernetes_service_accounts" {
  description = "Kubernetes service accounts given object read/write on the bucket"
  type = list(object({
    namespace = string
    name      = string
  }))
}
