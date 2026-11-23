#!/bin/sh
# Deploy (or update) the sites on the GKE cluster.
#
# Usage: deploy/gke/apply.sh IMAGE
#   IMAGE is what deploy/genshin-web/build.sh printed
#   (REGION-docker.pkg.dev/PROJECT/genshin/genshin-web:TAG).
#
# It renders deploy/overlays/prod with Kustomize, fills in the placeholders
# that would otherwise put the project id in the repository (image path,
# bucket, hostname) from Terraform's outputs, and applies the result with
# kubectl. kubectl must already point at the cluster: run the command in
# `terraform -chdir=terraform/envs/prod output kubectl_credentials`.
#
# Later (design doc phase 7) Argo CD replaces this script.
set -eu

IMAGE=${1:?usage: apply.sh IMAGE}
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
TF="terraform -chdir=$ROOT/terraform/envs/prod"

BUCKET=$($TF output -raw runs_bucket)
HOST=$($TF output -json hostnames | python3 -c 'import json,sys; print(json.load(sys.stdin)["genshin"])')
REPO=${IMAGE%/genshin-web:*}
TAG=${IMAGE##*:}
[ "$REPO/genshin-web:$TAG" = "$IMAGE" ] || { echo "unexpected image name: $IMAGE" >&2; exit 1; }

kubectl kustomize "$ROOT/deploy/overlays/prod" |
  sed -e "s|IMAGE_REPOSITORY|$REPO|g" \
      -e "s|IMAGE_TAG|$TAG|g" \
      -e "s|BUCKET_NAME|$BUCKET|g" \
      -e "s|GENSHIN_HOSTNAME|$HOST|g" |
  kubectl apply -f -

kubectl -n genshin rollout status deployment/genshin-web --timeout=10m
echo "https://$HOST"
