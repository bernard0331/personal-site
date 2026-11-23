variable "project" {
  description = "GCP project id (from a gitignored terraform.tfvars, never the repository)"
  type        = string
}

variable "region" {
  description = "Region for the state bucket"
  type        = string
  default     = "us-central1"
}
