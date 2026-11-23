#!/bin/sh
# Take everything in the cluster down, in the order that leaves nothing
# billing behind, before Terraform deletes the cluster itself.
#
# Usage: deploy/gke/down.sh
#
# The Gateway's load balancer (forwarding rules, URL maps, backend services,
# network endpoint groups) is made by GKE's controllers, not Terraform. If the
# cluster went first, those would be orphaned and the forwarding rule would
# keep billing (about $18 a month). So: stop any canary, delete the sites and
# the Gateway, and wait until Google Cloud shows no forwarding rule. Then
# switch GKE off in terraform/envs/prod/switches.auto.tfvars and apply.
set -eu
ROOT=$(cd "$(dirname "$0")/../.." && pwd)

if kubectl -n genshin get deployment genshin-web-canary >/dev/null 2>&1; then
  "$ROOT/deploy/gke/canary.sh" rollback
fi
kubectl delete -k "$ROOT/deploy/mesh" --ignore-not-found 2>/dev/null || true
kubectl kustomize "$ROOT/deploy/overlays/prod" | kubectl delete --ignore-not-found -f -

echo "waiting for the load balancer to be deleted..."
while [ -n "$(gcloud compute forwarding-rules list --format='value(name)' 2>/dev/null)" ]; do
  sleep 10
done
while [ -n "$(gcloud compute network-endpoint-groups list --format='value(name)' 2>/dev/null)" ]; do
  sleep 10
done
echo "no forwarding rules or endpoint groups left."
echo "Next: set gke_enabled = false and mesh_enabled = false in"
echo "terraform/envs/prod/switches.auto.tfvars, then: terraform -chdir=terraform/envs/prod apply"
