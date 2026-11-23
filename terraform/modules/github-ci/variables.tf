variable "repository" {
  description = "GitHub repository, owner/name (for descriptions)"
  type        = string
}

variable "repository_id" {
  description = "GitHub repository's numeric id (gh api repos/OWNER/NAME --jq .id)"
  type        = string
}

variable "state_bucket" {
  description = "Bucket holding Terraform's state"
  type        = string
}
