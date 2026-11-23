#!/bin/sh
# Deploy (or update) the sites on the GKE cluster.
#
# Usage: deploy/gke/apply.sh IMAGE
#   IMAGE is one of the images build.sh printed
#   (REGION-docker.pkg.dev/PROJECT/genshin/NAME:TAG); every site's image is
#   taken from the same repository with the same tag (the commit).
#
# It renders deploy/overlays/prod with Kustomize, fills in the placeholders
# that would otherwise put the project id in the repository (image path,
# bucket, and each site's <SITE>_HOSTNAME) from Terraform's outputs, and
# applies the result with kubectl. kubectl must already point at the cluster: run the command in
# `terraform -chdir=terraform/envs/prod output kubectl_credentials`.
#
# Later (design doc phase 7) Argo CD replaces this script.
set -eu

IMAGE=${1:?usage: apply.sh IMAGE}
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
TF="terraform -chdir=$ROOT/terraform/envs/prod"

BUCKET=$($TF output -raw runs_bucket)
NAME_TAG=${IMAGE##*/}
REPO=${IMAGE%/*}
TAG=${NAME_TAG##*:}
[ "$REPO/${NAME_TAG%%:*}:$TAG" = "$IMAGE" ] && [ "$TAG" != "$NAME_TAG" ] ||
  { echo "unexpected image name: $IMAGE" >&2; exit 1; }

# One sed expression per site: s|GENSHIN_HOSTNAME|genshin.1-2-3-4.sslip.io|g
HOSTS=$($TF output -json hostnames | python3 -c '
import json, sys
for site, host in json.load(sys.stdin).items():
    print("s|%s_HOSTNAME|%s|g" % (site.upper().replace("-", "_"), host))')

kubectl kustomize "$ROOT/deploy/overlays/prod" |
  sed -e "s|IMAGE_REPOSITORY|$REPO|g" \
      -e "s|IMAGE_TAG|$TAG|g" \
      -e "s|BUCKET_NAME|$BUCKET|g" \
      -e "$HOSTS" |
  kubectl apply -f -

# Wait for every site's Deployment (one site per namespace).
python3 -c 'import json; [print(s["namespace"], s["name"]) for s in json.load(open("'"$ROOT"'/terraform/envs/prod/services.json"))["services"]]' |
  while read -r NS NAME; do
    kubectl -n "$NS" rollout status "deployment/$NAME" --timeout=10m
  done
$TF output -json hostnames | python3 -c 'import json,sys; [print("https://" + h) for h in json.load(sys.stdin).values()]'
