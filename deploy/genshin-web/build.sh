#!/bin/sh
# Build the genshin-web container image with Cloud Build and push it to
# Artifact Registry.
#
# Usage: deploy/genshin-web/build.sh
#
# The image needs files that are not in the repository (local/ is gitignored):
# the cached KQM Sim Database (no licence to redistribute it in the repo),
# the weapon ranking, and HoYoverse's images. So this script assembles a build
# context outside the repository, uploads it to Cloud Build, and prints the
# image name to pass to deploy.sh. Nothing local-only is ever committed.
#
# Settings come from the environment, never the repository:
#   PROJECT  (default: gcloud's current project)
#   REGION   (default: us-central1)
#   REPO     Artifact Registry repository (default: genshin)
#   RANKING  weapon ranking file (default: local/archive/kqm-energy/ranking.json)
set -eu

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT"
PROJECT=${PROJECT:-$(gcloud config get-value project 2>/dev/null)}
REGION=${REGION:-us-central1}
REPO=${REPO:-genshin}
RANKING=${RANKING:-local/archive/kqm-energy/ranking.json}
TAG=$(git rev-parse --short HEAD)$(git diff --quiet HEAD -- services deploy || echo -dirty)
IMAGE=$REGION-docker.pkg.dev/$PROJECT/$REPO/genshin-web:$TAG

CTX=$(mktemp -d)
trap 'rm -rf "$CTX"' EXIT
# The repository's tracked files as they are in the working tree.
mkdir -p "$CTX/src" "$CTX/data"
git ls-files -z | xargs -0 -I{} rsync -R "{}" "$CTX/src/"
cp local/rotations/kqm-db.jsonl "$CTX/data/kqm-db.jsonl"
cp "$RANKING" "$CTX/data/ranking.json"
cp -R local/web-img "$CTX/data/img"
cp deploy/genshin-web/Dockerfile "$CTX/Dockerfile"
echo "context: $(du -sh "$CTX" | cut -f1)"

gcloud builds submit "$CTX" --project "$PROJECT" --region "$REGION" --tag "$IMAGE"
echo "$IMAGE"
