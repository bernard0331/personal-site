#!/bin/sh
# Build the landing page's image with Cloud Build and push it to Artifact
# Registry, tagged with the commit (like genshin-web's, so deploy/gke/apply.sh
# can deploy both with one tag).
#
# Usage: deploy/home/build.sh
#   PROJECT  (default: gcloud's current project)
#   REGION   (default: us-central1)
#   REPO     Artifact Registry repository (default: genshin)
set -eu

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT"
PROJECT=${PROJECT:-$(gcloud config get-value project 2>/dev/null)}
REGION=${REGION:-us-central1}
REPO=${REPO:-genshin}
TAG=$(git rev-parse --short HEAD)$(git diff --quiet HEAD -- services deploy || echo -dirty)
IMAGE=$REGION-docker.pkg.dev/$PROJECT/$REPO/home:$TAG

# Only what the image needs: go.mod and the service's own folder.
CTX=$(mktemp -d)
trap 'rm -rf "$CTX"' EXIT
mkdir -p "$CTX/services"
cp go.mod "$CTX/"
cp -R services/home "$CTX/services/home"
cp services/home/Dockerfile "$CTX/Dockerfile"

gcloud builds submit "$CTX" --project "$PROJECT" --region "$REGION" --tag "$IMAGE"
echo "$IMAGE"
