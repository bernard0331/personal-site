#!/bin/sh
# Prove the NetworkPolicies from inside real pods: what must connect does,
# and what must not, doesn't.
#
# Usage: deploy/gke/verify-policies.sh IMAGE
#   IMAGE is any image with python3 that the nodes can pull (genshin-web's).
#
# Each check runs in an ephemeral debug container added to a site's pod; it
# shares the pod's network, so the pod's policies apply to it. A pod in a
# throwaway namespace with no policies plays an outsider. Prints PASS/FAIL
# per check and exits non-zero if any check fails.
set -eu
IMAGE=${1:?usage: verify-policies.sh IMAGE}

# probe: python3 that tries each "host port expect" line (expect: open or
# blocked) with a 4-second timeout and prints PASS/FAIL lines.
PROBE='
import socket, sys
fails = 0
for line in sys.argv[1].strip().splitlines():
    host, port, expect, why = line.split(None, 3)
    try:
        socket.create_connection((host, int(port)), timeout=4).close()
        got = "open"
    except OSError as e:
        got = "blocked"
    ok = got == expect
    fails += not ok
    print("PASS" if ok else "FAIL", why, "(" + host + ":" + port, "is", got + ")")
sys.exit(1 if fails else 0)
'

run_in_pod() { # NAMESPACE APP CHECKS
  pod=$(kubectl -n "$1" get pod -l "app=$2" -o jsonpath='{.items[0].metadata.name}')
  name=probe-$(date +%s)
  kubectl -n "$1" debug "$pod" -q --profile=restricted --image="$IMAGE" -c "$name" \
    -- python3 -c "$PROBE" "$3" >/dev/null
  kubectl -n "$1" wait "pod/$pod" --timeout=120s \
    --for=jsonpath="{.status.ephemeralContainerStatuses[?(@.name==\"$name\")].state.terminated}" >/dev/null 2>&1 || true
  kubectl -n "$1" logs "$pod" -c "$name"
}

HOME_POD_IP=$(kubectl -n home get pod -l app=home -o jsonpath='{.items[0].status.podIP}')
GENSHIN_POD_IP=$(kubectl -n genshin get pod -l app=genshin-web -o jsonpath='{.items[0].status.podIP}')
KUBE_API=$(kubectl get svc kubernetes -o jsonpath='{.spec.clusterIP}')
status=0

echo "== from genshin-web (namespace genshin)"
run_in_pod genshin genshin-web "
storage.googleapis.com 443 open Google APIs through private.googleapis.com
199.36.153.4 443 blocked restricted.googleapis.com, not on the allow list
169.254.169.254 80 open metadata server (Workload Identity tokens)
$HOME_POD_IP 8080 blocked another site's pod
$KUBE_API 443 blocked the Kubernetes API
" || status=1

echo "== from home (namespace home)"
run_in_pod home home "
genshin-web.genshin.svc.cluster.local 80 open genshin-web's Service (allowed caller)
$GENSHIN_POD_IP 8080 open genshin-web's pod (allowed caller)
storage.googleapis.com 443 open Google APIs through private.googleapis.com
$KUBE_API 443 blocked the Kubernetes API
" || status=1

echo "== from an outsider (namespace policy-test, no policies of its own)"
kubectl create namespace policy-test --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n policy-test run probe --image="$IMAGE" --restart=Never --quiet \
  --overrides='{"spec":{"securityContext":{"runAsNonRoot":true,"runAsUser":10001,"seccompProfile":{"type":"RuntimeDefault"}}}}' \
  --command -- python3 -c "$PROBE" "
genshin-web.genshin.svc.cluster.local 80 blocked genshin-web from an unlisted namespace
$GENSHIN_POD_IP 8080 blocked genshin-web's pod from an unlisted namespace
home.home.svc.cluster.local 80 blocked home from an unlisted namespace
" >/dev/null
kubectl -n policy-test wait pod/probe --for=jsonpath='{.status.phase}'=Succeeded --timeout=180s >/dev/null 2>&1 ||
  kubectl -n policy-test wait pod/probe --for=jsonpath='{.status.phase}'=Failed --timeout=10s >/dev/null 2>&1 || true
kubectl -n policy-test logs probe
[ "$(kubectl -n policy-test get pod probe -o jsonpath='{.status.phase}')" = Succeeded ] || status=1
kubectl delete namespace policy-test --wait=false >/dev/null

exit $status
