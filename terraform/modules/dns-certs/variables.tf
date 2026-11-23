variable "name" {
  description = "Name of the address, certificate and certificate map"
  type        = string
}

variable "sites" {
  description = "One hostname per site: <site>.<ip with dashes>.sslip.io"
  type        = list(string)
}
