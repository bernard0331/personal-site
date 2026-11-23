variable "membership_id" {
  description = "The cluster's fleet membership id (modules/gke output fleet_membership)"
  type        = string
}

variable "membership_location" {
  type = string
}
