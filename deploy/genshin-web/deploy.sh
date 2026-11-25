#!/bin/sh
# Deploy (or update) the genshin-web Cloud Run service, and the Cloud Run Job
# its simulations fan out to (genshin-sim, the same image in -worker mode).
#
# Usage: deploy/genshin-web/deploy.sh IMAGE
#   IMAGE is what build.sh printed.
#
# Settings come from the environment, never the repository:
#   PROJECT  (default: gcloud's current project)
#   REGION   (default: us-central1)
#   BUCKET   Cloud Storage bucket for runs and the cache's copy (required)
#   SA       service account the service and the job run as (default: genshin-web@PROJECT.iam.gserviceaccount.com)
#   FANOUT_SHARDS  most workers one batch may use (default 8; 0 turns fan-out off)
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
#
# The job (genshin-sim):
#   --cpu 8 --memory 4Gi  Eight quick simulations at once, or one refined team
#                         with all eight cores (4 GiB is Cloud Run's minimum
#                         for 8 vCPU).
#   --tasks 1 --parallelism 0  Each execution sets its own task count (one per
#                         shard); 0 runs them all at once.
#   --task-timeout 30m --max-retries 1  A failed task is retried once; the
#                         site simulates whatever never comes back itself.
#   --args=-worker=none   Run without the site's overrides, a task exits at
#                         once instead of starting a web server.
#   The service account may start it with overrides (roles/run.jobsExecutorWithOverrides
#   on this job only); it already has the bucket, where batches go under fanout/.
set -eu

IMAGE=${1:?usage: deploy.sh IMAGE}
PROJECT=${PROJECT:-$(gcloud config get-value project 2>/dev/null)}
REGION=${REGION:-us-central1}
BUCKET=${BUCKET:?set BUCKET to the Cloud Storage bucket for runs}
SA=${SA:-genshin-web@$PROJECT.iam.gserviceaccount.com}

FANOUT_SHARDS=${FANOUT_SHARDS:-8}

gcloud run jobs deploy genshin-sim \
  --project "$PROJECT" --region "$REGION" \
  --image "$IMAGE" \
  --service-account "$SA" \
  --cpu 8 --memory 4Gi \
  --tasks 1 --parallelism 0 \
  --task-timeout 30m --max-retries 1 \
  --args=-worker=none

gcloud run jobs add-iam-policy-binding genshin-sim \
  --project "$PROJECT" --region "$REGION" \
  --member "serviceAccount:$SA" --role roles/run.jobsExecutorWithOverrides >/dev/null

FANOUT_STORE=""
if [ "$FANOUT_SHARDS" -gt 0 ]; then
  FANOUT_STORE="gs://$BUCKET/fanout"
fi

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
  --update-env-vars "FANOUT_STORE=$FANOUT_STORE,FANOUT_LAUNCH=cloudrun:projects/$PROJECT/locations/$REGION/jobs/genshin-sim,FANOUT_SHARDS=$FANOUT_SHARDS,FANOUT_WORKER_CPUS=8" \
  --allow-unauthenticated
