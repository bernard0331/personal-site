# The production environment: everything the GKE version of the site needs.
#
#   network    VPC and subnet with pod and Service ranges
#   gke        the zonal cluster, its spot node pool and the nodes' identity
#   registry   Artifact Registry repository for images      (imported)
#   storage    the runs bucket                               (imported)
#   iam        bucket access for Cloud Run and for the pod   (partly imported)
#   dns_certs  the Gateway's IP address, hostnames and TLS certificate
#
# What runs inside the cluster (Deployment, Service, Gateway, routes) is not
# here: it's in deploy/ as Kubernetes manifests. Terraform owns the cloud
# resources; the manifests own the cluster's contents.

locals {
  name = "personal-site"
}

module "network" {
  source = "../../modules/network"
  name   = local.name
  region = var.region
}

module "gke" {
  source              = "../../modules/gke"
  project             = var.project
  name                = local.name
  zone                = var.zone
  network             = module.network.network
  subnetwork          = module.network.subnetwork
  pods_range          = module.network.pods_range
  services_range      = module.network.services_range
  machine_type        = var.machine_type
  node_count          = var.node_count
  authorized_networks = var.authorized_networks
}

module "registry" {
  source      = "../../modules/registry"
  name        = "genshin"
  region      = var.region
  description = "genshin-web container images"
}

module "storage" {
  source = "../../modules/storage"
  name   = var.runs_bucket
  region = var.region
}

module "iam" {
  source        = "../../modules/iam"
  bucket        = module.storage.name
  workload_pool = module.gke.workload_pool
  kubernetes_service_accounts = [
    { namespace = "genshin", name = "genshin-web" },
  ]
}

module "dns_certs" {
  source = "../../modules/dns-certs"
  name   = "sites"
  sites  = ["genshin"]
}
