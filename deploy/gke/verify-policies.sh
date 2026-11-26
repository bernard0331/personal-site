#!/bin/sh
# Prove the NetworkPolicies from inside real pods: what must connect does,
# and what must not, doesn't.
#
# Usage: deploy/gke/verify-policies.sh IMAGE
#   IMAGE is any image with python3 that the nodes can pull (genshin-web's).
#
# Each check runs in an ephemeral debug container added to a site's pod; it
# shares the pod's network, so the pod's policies apply to it (and, with the
# service mesh on, its sidecar). A check counts as open only if the far end
# answers (TLS handshake or HTTP response), not just if a connection opens. A pod in a
# throwaway namespace with no policies plays an outsider. Prints PASS/FAIL
# per check and exits non-zero if any check fails.
set -eu
IMAGE=${1:?usage: verify-policies.sh IMAGE}

# probe: python3 that tries each "host port expect" line (expect: open or
# blocked) with a 4-second timeout and prints PASS/FAIL lines.
PROBE='
import socket, ssl, sys

def reached(host, port):
    # A plain TCP connect is not enough: with the service mesh on, the pod
    # Envoy sidecar accepts every outgoing connection itself and only then
    # dials the destination. So require an answer from the far end: a
    # completed TLS handshake on 443, otherwise an HTTP response that is not
    # Envoy reporting it could not connect.
    s = socket.create_connection((host, port), timeout=4)
    s.settimeout(4)
    try:
        if port == 443:
            ctx = ssl.create_default_context()
            ctx.check_hostname, ctx.verify_mode = False, ssl.CERT_NONE
            ctx.wrap_socket(s, server_hostname=host).close()
            return True
        s.sendall(("GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n" % host).encode())
        data = b""
        while len(data) < 4096:
            chunk = s.recv(4096)
            if not chunk:
                break
            data += chunk
        return data.startswith(b"HTTP/") and b"upstream connect error" not in data
    finally:
        s.close()

fails = 0
for line in sys.argv[1].strip().splitlines():
    host, port, expect, why = line.split(None, 3)
    try:
        got = "open" if reached(host, int(port)) else "blocked"
    except OSError:
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
$KUBE_API 443 open the Kubernetes API (fan-out starts worker Jobs; RBAC checked below)
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

# What the site's service account may do with the API it can now reach
# (deploy/base/genshin-web/fanout.yaml): Jobs in its own namespace, nothing else.
echo "== the site's Kubernetes permissions (RBAC)"
SA=system:serviceaccount:genshin:genshin-web
while read -r want verb what ns why; do
  [ -z "$verb" ] && continue
  got=$(kubectl auth can-i "$verb" "$what" -n "$ns" --as "$SA" 2>/dev/null || true)
  if [ "$got" = "$want" ]; then mark=ok; else mark=FAIL; status=1; fi
  printf '%-4s %-3s %s %s in %s (%s)\n' "$mark" "$got" "$verb" "$what" "$ns" "$why"
done <<EOF2
yes create jobs.batch genshin starting worker Jobs
yes get pods genshin reading its own pod's image
no create pods genshin pods only through Jobs
no get secrets genshin no Secrets
no create jobs.batch home only its own namespace
no delete deployments.apps genshin nothing else
EOF2

exit $status
