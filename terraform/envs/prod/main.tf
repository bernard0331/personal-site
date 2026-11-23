# The production environment: everything the GKE version of the site needs.
# The GKE parts (network, gke, dns_certs) exist only with gke_enabled = true;
# the rest is what the Cloud Run site uses and always stays.
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

  # The sites behind the Gateway. services.json is the one list of them; the
  # Go tool tools/sitectl adds entries (and scaffolds their manifests), and
  # Terraform gives each one a hostname, a certificate, a firewall port and,
  # if it asks for the runs bucket, Workload Identity access to it.
  services = jsondecode(file("${path.module}/services.json")).services

  # 1 when the GKE copy is on, 0 when off. count = 0 on a module means none
  # of its resources exist; switching to 0 and applying destroys them.
  gke = var.gke_enabled ? 1 : 0
}

module "network" {
  count     = local.gke
  source    = "../../modules/network"
  name      = local.name
  region    = var.region
  app_ports = distinct([for s in local.services : s.port])
}

module "gke" {
  count               = local.gke
  source              = "../../modules/gke"
  project             = var.project
  name                = local.name
  zone                = var.zone
  network             = module.network[0].network
  subnetwork          = module.network[0].subnetwork
  pods_range          = module.network[0].pods_range
  services_range      = module.network[0].services_range
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
  source = "../../modules/iam"
  bucket = module.storage.name
  # The pod's bucket access exists only while the cluster does.
  workload_pool = one(module.gke[*].workload_pool)
  kubernetes_service_accounts = var.gke_enabled ? [
    for s in local.services : { namespace = s.namespace, name = s.name } if s.bucket
  ] : []
}

module "dns_certs" {
  count  = local.gke
  source = "../../modules/dns-certs"
  name   = "sites"
  sites  = [for s in local.services : s.site]
}
