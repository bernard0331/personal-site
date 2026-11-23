#!/bin/sh
# Prove the service mesh's two promises with real traffic:
#   1. home -> genshin-web travels over mutual TLS, with home's identity
#   2. a caller the NetworkPolicies let through, but with another identity,
#      is refused by genshin-web's AuthorizationPolicy
#
# Usage: deploy/gke/verify-mesh.sh IMAGE   (any image with python3)
# Needs mesh_enabled and pods started after the mesh came up.
set -eu
IMAGE=${1:?usage: verify-mesh.sh IMAGE}
HOST=$(terraform -chdir="$(dirname "$0")/../../terraform/envs/prod" output -json hostnames | python3 -c 'import json,sys; print(json.load(sys.stdin)["www"])')
status=0

# Some traffic from home: each landing page view checks genshin-web's health.
for i in 1 2 3; do curl -s -o /dev/null "https://$HOST/"; done

echo "== requests genshin-web received, from its sidecar's metrics"
POD=$(kubectl -n genshin get pod -l app=genshin-web -o jsonpath='{.items[0].metadata.name}')
kubectl -n genshin exec "$POD" -c istio-proxy -- pilot-agent request GET stats/prometheus 2>/dev/null |
  grep '^istio_requests_total' | grep 'reporter="destination"' | python3 -c '
import re, sys
mtls = False
for line in sys.stdin:
    d = dict(re.findall(r"(\w+)=\"([^\"]*)\"", line))
    principal = re.sub(r"^spiffe://[^/]+", "spiffe://<trust domain>", d.get("source_principal", ""))
    print(" from", principal, "| security:", d.get("connection_security_policy"), "| code:", d.get("response_code"), "| requests:", line.rsplit(" ", 1)[1].strip())
    mtls |= principal.endswith("/ns/home/sa/home") and d.get("connection_security_policy") == "mutual_tls"
print("PASS home calls genshin-web over mutual TLS" if mtls else "FAIL no mutual-TLS requests from home")
sys.exit(0 if mtls else 1)' || status=1

echo "== a caller with the wrong identity (namespace home, label app=home, service account default)"
kubectl -n home delete pod impostor --ignore-not-found >/dev/null
kubectl -n home run impostor --image="$IMAGE" --restart=Never --labels=app=home --quiet \
  --overrides='{"metadata":{"annotations":{"proxy.istio.io/config":"{\"holdApplicationUntilProxyStarts\": true}","sidecar.istio.io/proxyCPU":"50m","sidecar.istio.io/proxyMemory":"64Mi"}},"spec":{"serviceAccountName":"default","securityContext":{"runAsNonRoot":true,"runAsUser":10001,"seccompProfile":{"type":"RuntimeDefault"}}}}' \
  --command -- python3 -c '
import urllib.request, urllib.error
try:
    urllib.request.urlopen("http://genshin-web.genshin.svc.cluster.local/healthz", timeout=5)
    print("FAIL the impostor was let in")
except urllib.error.HTTPError as e:
    print(("PASS" if e.code == 403 else "FAIL"), "the impostor got", e.code, e.read()[:40].decode())
except Exception as e:
    print("FAIL", e)
' >/dev/null
kubectl -n home wait pod/impostor --timeout=240s \
  --for=jsonpath='{.status.containerStatuses[?(@.name=="impostor")].state.terminated.reason}'=Completed >/dev/null 2>&1 || true
OUT=$(kubectl -n home logs impostor -c impostor)
echo " $OUT"
case $OUT in PASS*) ;; *) status=1 ;; esac
kubectl -n home delete pod impostor --wait=false >/dev/null
exit $status
