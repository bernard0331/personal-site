# The Gateway's address and TLS certificate.
#
# A reserved global IP address, so the address (and the hostnames built from
# it) survive the Gateway being deleted and created again.
#
# Hostnames: until a domain is bought, free wildcard DNS from sslip.io:
# genshin.34-1-2-3.sslip.io resolves to 34.1.2.3, so each site gets a real
# hostname pointing at the Gateway with no DNS zone to manage.
#
# Certificates: one Google-managed certificate per site from Certificate
# Manager, with load balancer authorization: Google proves control of the
# hostname by answering for it on the load balancer itself (the hostname
# already points there), so no DNS records are needed. One per site, not one
# for all: adding a site then issues only its own certificate and leaves the
# others serving. Each is issued once the Gateway serves its hostname,
# usually within an hour. The Gateway finds them through a certificate map
# (annotation networking.gke.io/certmap), one entry per hostname.
#
# Let's Encrypt would also work, but sslip.io is one shared domain for
# everyone using it, so its rate limits are often used up.

resource "google_compute_global_address" "gateway" {
  name = var.name
}

locals {
  ip_dashed = replace(google_compute_global_address.gateway.address, ".", "-")
  hostnames = { for site in var.sites : site => "${site}.${local.ip_dashed}.sslip.io" }
}

resource "google_certificate_manager_certificate" "sites" {
  for_each = local.hostnames
  name     = "${var.name}-${each.key}"
  scope    = "DEFAULT"
  managed {
    domains = [each.value]
  }
}

resource "google_certificate_manager_certificate_map" "this" {
  name = var.name
}

resource "google_certificate_manager_certificate_map_entry" "sites" {
  for_each     = local.hostnames
  name         = "${var.name}-${each.key}"
  map          = google_certificate_manager_certificate_map.this.name
  hostname     = each.value
  certificates = [google_certificate_manager_certificate.sites[each.key].id]
}
