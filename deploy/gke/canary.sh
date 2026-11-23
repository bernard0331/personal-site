#!/bin/sh
# Canary releases of genshin-web on GKE: a new image takes a share of new
# visitors next to the stable version, and is then promoted or rolled back.
#
#   canary.sh start IMAGE [PERCENT]  run IMAGE as the canary, send PERCENT
#                                    (default 10) of new visitors to it
#   canary.sh weight PERCENT         change the share (0-100)
#   canary.sh promote                make the canary's image stable, then
#                                    remove the canary
#   canary.sh rollback               send everyone to stable, remove the canary
#   canary.sh status                 versions, pods and the current split
#
# How traffic is split: deploy/canary/genshin-web/httproute.yaml. Visitors
# are sticky (a track cookie), because each version keeps its runs to
# itself; the weights decide only where new visitors go.
#
# kubectl must point at the cluster, and deploy/gke/apply.sh must have
# deployed the stable version first.
set -eu

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
TF="terraform -chdir=$ROOT/terraform/envs/prod"
NS=genshin

host() { $TF output -json hostnames | python3 -c 'import json,sys; print(json.load(sys.stdin)["genshin"])'; }

# route PERCENT: the canary route with PERCENT of new visitors to the canary.
route() {
  case $1 in '' | *[!0-9]*) echo "percent must be 0-100, not '$1'" >&2; exit 1 ;; esac
  [ "$1" -le 100 ] || { echo "percent must be 0-100" >&2; exit 1; }
  sed -e "s|GENSHIN_HOSTNAME|$(host)|" \
      -e "s|STABLE_WEIGHT|$((100 - $1))|" \
      -e "s|CANARY_WEIGHT|$1|" \
      "$ROOT/deploy/canary/genshin-web/httproute.yaml" | kubectl apply -f -
  echo "new visitors: $((100 - $1))% stable, $1% canary"
}

# stable_route: the base route (everyone to stable), as apply.sh deploys it.
stable_route() {
  sed -e "s|GENSHIN_HOSTNAME|$(host)|" "$ROOT/deploy/base/genshin-web/httproute.yaml" |
    kubectl apply -n "$NS" -f -
}

# all_to_canary: every visitor, cookie or not, to the canary.
all_to_canary() {
  sed -e "s|GENSHIN_HOSTNAME|$(host)|" \
      -e "s|^        - name: genshin-web\$|        - name: genshin-web-canary|" \
      "$ROOT/deploy/base/genshin-web/httproute.yaml" | kubectl apply -n "$NS" -f -
}

canary_image() {
  kubectl -n "$NS" get deployment genshin-web-canary -o jsonpath='{.spec.template.spec.containers[0].image}'
}

remove_canary() {
  kubectl -n "$NS" delete deployment/genshin-web-canary service/genshin-web-canary \
    healthcheckpolicy/genshin-web-canary --ignore-not-found
}

case ${1:-} in
start)
  IMAGE=${2:?usage: canary.sh start IMAGE [PERCENT]}
  PERCENT=${3:-10}
  REPO=${IMAGE%/genshin-web:*}
  TAG=${IMAGE##*:}
  [ "$REPO/genshin-web:$TAG" = "$IMAGE" ] || { echo "unexpected image name: $IMAGE" >&2; exit 1; }
  BUCKET=$($TF output -raw runs_bucket)

  # The canary keeps its own bucket folder; start its cache from stable's so
  # it doesn't simulate everything again.
  if ! gcloud storage ls "gs://$BUCKET/gke-canary/cache.jsonl" >/dev/null 2>&1; then
    gcloud storage cp "gs://$BUCKET/gke/cache.jsonl" "gs://$BUCKET/gke-canary/cache.jsonl" || true
  fi

  kubectl kustomize "$ROOT/deploy/canary/genshin-web" |
    sed -e "s|IMAGE_REPOSITORY|$REPO|g" -e "s|CANARY_TAG|$TAG|g" -e "s|BUCKET_NAME|$BUCKET|g" |
    kubectl apply -f -
  kubectl -n "$NS" rollout status deployment/genshin-web-canary --timeout=10m
  route "$PERCENT"
  ;;
weight)
  kubectl -n "$NS" get deployment genshin-web-canary >/dev/null
  route "${2:?usage: canary.sh weight PERCENT}"
  ;;
promote)
  IMAGE=$(canary_image)
  # Everyone to the canary while stable is replaced (stable's Deployment
  # stops its old pod before starting the new one), so pages keep loading;
  # then everyone back to the new stable and the canary goes. A run started
  # on stable can't be followed during the swap (about a minute) and shows
  # again after it.
  all_to_canary
  kubectl -n "$NS" set image deployment/genshin-web "genshin-web=$IMAGE"
  kubectl -n "$NS" rollout status deployment/genshin-web --timeout=10m
  stable_route
  remove_canary
  echo "promoted $IMAGE. Run deploy/gke/apply.sh $IMAGE too, or the next apply reverts it."
  ;;
rollback)
  stable_route
  remove_canary
  echo "rolled back: everyone on stable"
  ;;
status)
  kubectl -n "$NS" get deployments -l app=genshin-web \
    -o custom-columns='NAME:.metadata.name,IMAGE:.spec.template.spec.containers[0].image,READY:.status.readyReplicas'
  kubectl -n "$NS" get httproute genshin-web \
    -o jsonpath='{range .spec.rules[-1].backendRefs[*]}{.name}{" weight "}{.weight}{"\n"}{end}'
  ;;
*)
  sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
  ;;
esac
