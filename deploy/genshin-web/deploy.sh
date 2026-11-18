#!/bin/sh
# Deploy (or update) the genshin-web Cloud Run service.
#
# Usage: deploy/genshin-web/deploy.sh IMAGE
#   IMAGE is what build.sh printed.
#
# Settings come from the environment, never the repository:
#   PROJECT  (default: gcloud's current project)
#   REGION   (default: us-central1)
#   BUCKET   Cloud Storage bucket for runs and the cache's copy (required)
#   SA       service account the service runs as (default: genshin-web@PROJECT.iam.gserviceaccount.com)
#
# Why each setting:
#   --max-instances 1     One instance holds the run queue and the per-visitor
#                         limits in memory, so there must never be two. It also
#                         caps the bill: heavy use lengthens the queue instead.
#   --min-instances 0     No instance (and no cost) while nobody uses the site.
#   --cpu 8 --memory 8Gi  Seven simulations at once plus the server and solver;
#                         each simulation may use up to 400 MB.
#   --no-cpu-throttling   Runs continue after the request that started them
#                         returns, so the CPU must stay on between requests
#                         (instance-based billing, while an instance is up).
#   --execution-environment gen2  Needed for the Cloud Storage volume.
#   --add-volume ...      The bucket mounted at /data (runs, the cache's copy).
#   --allow-unauthenticated  The site has no sign-in; anyone with the link can use it.
set -eu

IMAGE=${1:?usage: deploy.sh IMAGE}
PROJECT=${PROJECT:-$(gcloud config get-value project 2>/dev/null)}
REGION=${REGION:-us-central1}
BUCKET=${BUCKET:?set BUCKET to the Cloud Storage bucket for runs}
SA=${SA:-genshin-web@$PROJECT.iam.gserviceaccount.com}

gcloud run deploy genshin-web \
  --project "$PROJECT" --region "$REGION" \
  --image "$IMAGE" \
  --service-account "$SA" \
  --max-instances 1 --min-instances 0 \
  --cpu 8 --memory 8Gi --no-cpu-throttling \
  --concurrency 80 --timeout 300 \
  --execution-environment gen2 \
  --add-volume name=data,type=cloud-storage,bucket="$BUCKET" \
  --add-volume-mount volume=data,mount-path=/data \
  --port 8080 \
  --allow-unauthenticated
