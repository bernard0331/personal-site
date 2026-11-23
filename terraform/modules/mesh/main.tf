# Cloud Service Mesh, Google's managed Istio, for one fleet member cluster.
#
# MANAGEMENT_AUTOMATIC: Google runs the control plane (istiod's job, outside
# the cluster), installs the Istio CNI plugin on the nodes, issues each
# workload a certificate for its Kubernetes service account, and upgrades the
# sidecar proxies. Namespaces labelled istio-injection=enabled
# (deploy/components/mesh) get an Envoy sidecar in each pod. Sidecars talk
# mutual TLS to each other, and Istio AuthorizationPolicies (deploy/mesh)
# allow calls by identity.
#
# Billed per cluster in the mesh (about $50 a month, so about $0.07 an hour)
# plus endpoints over 100; the mesh exists only with mesh_enabled.

resource "google_gke_hub_feature" "mesh" {
  name     = "servicemesh"
  location = "global"
}

resource "google_gke_hub_feature_membership" "mesh" {
  location            = "global"
  feature             = google_gke_hub_feature.mesh.name
  membership          = var.membership_id
  membership_location = var.membership_location
  mesh {
    management = "MANAGEMENT_AUTOMATIC"
  }
}
