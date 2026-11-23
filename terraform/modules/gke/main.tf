# A zonal GKE Standard cluster with one spot node pool.
#
# Zonal, not regional: one control plane in one zone. The GKE free tier
# credit covers one zonal (or Autopilot) cluster's management fee, so the
# control plane costs nothing; a regional cluster would be billed.
#
# Standard, not Autopilot: this project is for learning Kubernetes, and
# Standard shows the nodes, node pools and their trade-offs.

# The nodes' own identity. By default nodes use the Compute Engine default
# service account, which has the project-wide Editor role; this one may only
# write logs and metrics and pull images. Pods don't use it: with Workload
# Identity each pod gets its own identity (see the iam module).
resource "google_service_account" "nodes" {
  account_id   = "${var.name}-nodes"
  display_name = "GKE nodes (${var.name})"
}

resource "google_project_iam_member" "nodes" {
  for_each = toset([
    "roles/logging.logWriter",
    "roles/monitoring.metricWriter",
    "roles/monitoring.viewer",
    "roles/stackdriver.resourceMetadata.writer",
    "roles/artifactregistry.reader",
  ])
  project = var.project
  role    = each.value
  member  = google_service_account.nodes.member
}

resource "google_container_cluster" "this" {
  name     = var.name
  location = var.zone

  # No default node pool: the node pool below is its own resource, so it can
  # be replaced or resized without touching the cluster. (Older GKE versions
  # needed remove_default_node_pool instead; the provider marks it deprecated
  # from 1.36.4.)
  initial_node_count = 0

  network         = var.network
  subnetwork      = var.subnetwork
  networking_mode = "VPC_NATIVE"
  ip_allocation_policy {
    cluster_secondary_range_name  = var.pods_range
    services_secondary_range_name = var.services_range
  }

  # Dataplane V2: eBPF networking (Cilium), which also enforces Kubernetes
  # NetworkPolicies (design doc phase 5) with no extra add-on.
  datapath_provider = "ADVANCED_DATAPATH"

  # Workload Identity: pods act as their Kubernetes service account, which is
  # granted Google Cloud roles directly. No service account keys anywhere.
  workload_identity_config {
    workload_pool = "${var.project}.svc.id.goog"
  }

  # Nodes get internal addresses only. The control plane keeps a public
  # endpoint so kubectl works from the laptop; every call to it still needs a
  # Google identity with access to the cluster.
  private_cluster_config {
    enable_private_nodes    = true
    enable_private_endpoint = false
  }

  # Optionally limit which addresses may reach the control plane at all.
  dynamic "master_authorized_networks_config" {
    for_each = length(var.authorized_networks) > 0 ? [1] : []
    content {
      dynamic "cidr_blocks" {
        for_each = var.authorized_networks
        content {
          cidr_block   = cidr_blocks.value
          display_name = cidr_blocks.value
        }
      }
    }
  }

  # The Gateway API (Gateway, HTTPRoute) and GKE's controller for it, which
  # turns a Gateway into a Google Cloud load balancer.
  gateway_api_config {
    channel = "CHANNEL_STANDARD"
  }

  addons_config {
    # Mounts a Cloud Storage bucket as a folder in a pod (the runs bucket at
    # /data, as on Cloud Run).
    gcs_fuse_csi_driver_config {
      enabled = true
    }
  }

  release_channel {
    channel = "REGULAR"
  }

  # Upgrades only overnight (US Central) on weekends, so a node isn't
  # replaced in the middle of someone's evening run.
  maintenance_policy {
    recurring_window {
      start_time = "2026-01-03T08:00:00Z"
      end_time   = "2026-01-03T14:00:00Z"
      recurrence = "FREQ=WEEKLY;BYDAY=SA,SU"
    }
  }

  deletion_protection = var.deletion_protection
}

# The nodes. Spot VMs cost a fraction of normal ones, and Google may take
# them back at any time with 30 seconds' notice. That's acceptable here: the
# site resumes unfinished runs on restart (from the bucket and the cache), and
# with two nodes the pod moves to the other one.
resource "google_container_node_pool" "spot" {
  name     = "spot"
  cluster  = google_container_cluster.this.id
  location = var.zone

  node_count = var.node_count

  management {
    auto_repair  = true
    auto_upgrade = true
  }

  node_config {
    spot         = true
    machine_type = var.machine_type
    # The default boot disk is a 100 GB balanced disk per node; the image and
    # logs need far less, and a standard disk is cheaper.
    disk_size_gb = var.disk_size_gb
    disk_type    = "pd-standard"

    service_account = google_service_account.nodes.email
    oauth_scopes    = ["https://www.googleapis.com/auth/cloud-platform"]

    # Pods see GKE's metadata server, which hands out their own Workload
    # Identity credentials instead of the node's.
    workload_metadata_config {
      mode = "GKE_METADATA"
    }

    shielded_instance_config {
      enable_secure_boot          = true
      enable_integrity_monitoring = true
    }
  }
}
