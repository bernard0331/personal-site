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
