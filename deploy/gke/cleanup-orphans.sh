#!/bin/sh
# After the cluster is deleted, remove what GKE and Cloud Service Mesh made
# outside Terraform and left behind on the VPC, so Terraform can delete it.
#
# Usage: deploy/gke/cleanup-orphans.sh   (then apply Terraform again)
#
# Found on 2026-11-23: deleting the cluster left empty network endpoint
# groups for its system services (k8s1-<id>-kube-system-..., gmp-system-...)
# and the mesh's reserved one (gsmrsvd-...), plus the mesh's health-check
# firewall rule (gke-csm-thc-...). None of them cost anything, but the VPC
# can't be deleted while they exist. Refuses to run while a cluster exists.
set -eu
NETWORK=${NETWORK:-personal-site}
ZONE=${ZONE:-us-central1-a}

if [ -n "$(gcloud container clusters list --format='value(name)')" ]; then
  echo "a cluster still exists; delete it first (switches off, terraform apply)" >&2
  exit 1
fi
for neg in $(gcloud compute network-endpoint-groups list --format='value(name)' | grep -E '^(k8s1-|gsmrsvd-)' || true); do
  gcloud compute network-endpoint-groups delete "$neg" --zone "$ZONE" --quiet
done
for rule in $(gcloud compute firewall-rules list --filter="network~/$NETWORK\$" --format='value(name)' | grep -E '^gke-' || true); do
  gcloud compute firewall-rules delete "$rule" --quiet
done
echo "done; now run: terraform -chdir=terraform/envs/prod apply"
