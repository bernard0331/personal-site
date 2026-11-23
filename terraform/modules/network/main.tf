# A custom-mode VPC with one subnet for the cluster. "Custom mode" means no
# subnet is created automatically in every region; this one is all there is.
#
# GKE gives every pod and every Service its own IP address (VPC-native
# networking), taken from two secondary ranges on the subnet:
#   primary range   nodes (the VMs)
#   "pods" range    pods
#   "services"      ClusterIP Services
#
# No Cloud NAT (design doc: it costs money). The nodes have no public
# addresses, so pods can't reach the internet; the site doesn't need to (its
# rotation database is baked into the image). Private Google Access lets the
# nodes still reach Google APIs: Artifact Registry to pull images, Cloud
# Storage for the runs bucket, Logging and Monitoring.

resource "google_compute_network" "this" {
  name                    = var.name
  auto_create_subnetworks = false
  routing_mode            = "REGIONAL"
}

resource "google_compute_subnetwork" "this" {
  name                     = "${var.name}-${var.region}"
  network                  = google_compute_network.this.id
  region                   = var.region
  ip_cidr_range            = var.nodes_cidr
  private_ip_google_access = true

  secondary_ip_range {
    range_name    = "pods"
    ip_cidr_range = var.pods_cidr
  }
  secondary_ip_range {
    range_name    = "services"
    ip_cidr_range = var.services_cidr
  }
}

# The Gateway's load balancer reaches pods from Google's front ends and checks
# their health from Google's health-check ranges; both are fixed, documented
# ranges. GKE adds its own rule for this too, but an explicit one makes the
# allowed path visible here. Only the port the site listens on.
resource "google_compute_firewall" "lb_to_pods" {
  name          = "${var.name}-allow-lb-to-pods"
  network       = google_compute_network.this.id
  direction     = "INGRESS"
  source_ranges = ["35.191.0.0/16", "130.211.0.0/22"]
  allow {
    protocol = "tcp"
    ports    = [tostring(var.app_port)]
  }
}

# Google APIs on private addresses. Private Google Access alone reaches
# Google APIs on their usual public addresses (many, and they change), which
# a NetworkPolicy can't list. Instead, a private DNS zone visible only in
# this VPC answers every *.googleapis.com name with private.googleapis.com,
# four fixed addresses (199.36.153.8/30) that are reachable only through
# Private Google Access, never the internet. The pods' egress policy
# (deploy/components/network-isolation) then allows exactly those four.
#
# A managed zone costs about $0.20 a month; it exists only with the cluster.

locals {
  private_googleapis = ["199.36.153.8", "199.36.153.9", "199.36.153.10", "199.36.153.11"]
}

resource "google_dns_managed_zone" "googleapis" {
  name        = "${var.name}-googleapis"
  dns_name    = "googleapis.com."
  description = "Google APIs on private.googleapis.com for the ${var.name} VPC"
  visibility  = "private"
  private_visibility_config {
    networks {
      network_url = google_compute_network.this.id
    }
  }
}

resource "google_dns_record_set" "private_googleapis" {
  managed_zone = google_dns_managed_zone.googleapis.name
  name         = "private.googleapis.com."
  type         = "A"
  ttl          = 300
  rrdatas      = local.private_googleapis
}

resource "google_dns_record_set" "googleapis_wildcard" {
  managed_zone = google_dns_managed_zone.googleapis.name
  name         = "*.googleapis.com."
  type         = "CNAME"
  ttl          = 300
  rrdatas      = [google_dns_record_set.private_googleapis.name]
}

# The route to those addresses. A new VPC already has a default route to the
# internet gateway, which covers them; this one says so explicitly and keeps
# them reachable if that default route is ever removed. (Traffic to them
# never leaves Google's network.)
resource "google_compute_route" "private_googleapis" {
  name             = "${var.name}-private-googleapis"
  network          = google_compute_network.this.id
  dest_range       = "199.36.153.8/30"
  next_hop_gateway = "default-internet-gateway"
  priority         = 900
}
