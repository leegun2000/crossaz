#!/usr/bin/env bash
# crossaz local e2e harness (kind). See hack/e2e/README.md.
#
#   hack/e2e/run.sh [up|l2|test|down|all]     default: all
#
#   up    create the kind cluster if missing, apply scenarios + traffic, wait for rollouts, hints and clients
#   l2    install Prometheus, start the volume generator, wait for the sink's cAdvisor series
#   test  go test -tags e2e ./hack/e2e/ (E2E_L2=1 when the Prometheus release exists, unless E2E_L2 is set)
#   down  delete the cluster
#   all   up, l2, test, then down unless KEEP_CLUSTER=1 (a failed test keeps the cluster)
#
# kind, kubectl, helm and the tests use only hack/e2e/.kubeconfig (never ~/.kube/config), and every
# kubectl/helm call names the context explicitly.
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ROOT=$(cd "$HERE/../.." && pwd)
MANIFESTS=$HERE/manifests

CLUSTER=crossaz-e2e
CONTEXT=kind-$CLUSTER
KIND_VERSION=v0.33.0
PROM_CHART_VERSION=29.30.0
PROM_REPO=https://prometheus-community.github.io/helm-charts
PROM_NAMESPACE=monitoring
WAIT=${E2E_WAIT:-300s}
POLL_TRIES=150 # x 2 s

export KUBECONFIG=$HERE/.kubeconfig
# helm lets these override the server and credentials of --kube-context; resolve only from KUBECONFIG.
unset HELM_KUBEAPISERVER HELM_KUBETOKEN HELM_KUBECAFILE HELM_KUBEASUSER HELM_KUBEASGROUPS \
  HELM_KUBETLS_SERVER_NAME HELM_KUBEINSECURE_SKIP_TLS_VERIFY HELM_KUBECONTEXT

log() { printf '==> %s\n' "$*" >&2; }
die() {
  printf 'run.sh: %s\n' "$*" >&2
  exit 1
}

kind() { go run "sigs.k8s.io/kind@$KIND_VERSION" "$@"; }
k() { kubectl --context "$CONTEXT" "$@"; }
h() { helm --kube-context "$CONTEXT" "$@"; }

require_tools() {
  local tool
  for tool in go docker kubectl helm jq; do
    command -v "$tool" >/dev/null || die "$tool not found in PATH"
  done
}

cluster_exists() {
  local clusters
  clusters=$(kind get clusters) || die "kind get clusters failed (is Docker running?)"
  grep -qx "$CLUSTER" <<<"$clusters"
}

# guard_context refuses to run unless $CONTEXT points at the API server of the kind cluster $CLUSTER.
guard_context() {
  local retry="run '$0 down' and retry (a half-created cluster?)"
  cluster_exists || die "kind cluster $CLUSTER does not exist: run '$0 up' first"
  local want got
  want=$(kind get kubeconfig --name "$CLUSTER" | awk '$1 == "server:" { print $2; exit }') ||
    die "cannot read the kubeconfig of kind cluster $CLUSTER: $retry"
  got=$(kubectl config view --minify --context "$CONTEXT" -o jsonpath='{.clusters[0].cluster.server}' 2>/dev/null || true)
  [[ -n "$want" && "$got" == "$want" ]] ||
    die "context $CONTEXT in $KUBECONFIG points at '$got', not kind cluster $CLUSTER ('$want'): refusing; $retry"
}

up() {
  if ! cluster_exists; then
    log "creating kind cluster $CLUSTER"
    # --name beats KIND_CLUSTER_NAME, which would otherwise replace the name in kind.yaml.
    kind create cluster --name "$CLUSTER" --config "$HERE/kind.yaml" --wait 3m
  fi
  guard_context

  log "applying scenarios and traffic servers"
  k apply -f "$MANIFESTS/scenarios.yaml"
  k apply -f "$MANIFESTS/traffic.yaml" -l e2e.crossaz.io/role=server
  local ns
  for ns in payments cache orders notify ingress-alb ingress-automode ingress-nginx edge traffic; do
    k -n "$ns" rollout status deployment --timeout "$WAIT"
  done
  k -n events rollout status statefulset/kafka --timeout "$WAIT"

  log "waiting for EndpointSlice hints"
  wait_endpoints orders api 6 6
  wait_endpoints ingress-nginx web 3 3
  wait_endpoints traffic spread-close 6 6
  wait_endpoints notify worker 4 0 # topology-mode Auto withholds hints for 4 endpoints

  log "running traffic clients"
  k apply -f "$MANIFESTS/traffic.yaml" -l e2e.crossaz.io/role=client
  k -n traffic wait --for=condition=complete job -l e2e.crossaz.io/role=client --timeout "$WAIT"
}

# wait_endpoints NS SVC READY HINTED waits until the Service has READY ready endpoints, HINTED of them with hints.
# A failed read (API hiccup, a slice with no endpoints yet) is retried like any other mismatch.
wait_endpoints() {
  local ns=$1 svc=$2 want="$3 $4" got="" i
  for ((i = 0; i < POLL_TRIES; i++)); do
    got=$(k -n "$ns" get endpointslices -l "kubernetes.io/service-name=$svc" -o json |
      jq -r '[.items[] | (.endpoints // [])[] | select(.conditions.ready)] | "\(length) \(map(select(.hints.forZones)) | length)"') ||
      got="read failed"
    if [[ "$got" == "$want" ]]; then return 0; fi
    sleep 2
  done
  die "$ns/$svc: ready/hinted endpoints '$got', want '$want'"
}

l2() {
  guard_context
  log "installing prometheus chart $PROM_CHART_VERSION"
  h upgrade --install prometheus prometheus --repo "$PROM_REPO" --version "$PROM_CHART_VERSION" \
    --namespace "$PROM_NAMESPACE" --create-namespace -f "$HERE/prometheus-values.yaml" --wait --timeout 5m

  log "starting volume sink and generator"
  k apply -f "$MANIFESTS/volume.yaml"
  k -n volume rollout status deployment --timeout "$WAIT"

  local pod query i
  pod=$(k -n volume get pods -l app=sink -o jsonpath='{.items[0].metadata.name}')
  query="container_network_receive_bytes_total{namespace=\"volume\",pod=\"$pod\"}"
  for ((i = 0; i < POLL_TRIES; i++)); do
    if [[ $(prom_query "$query" | jq '.data.result | length') -gt 0 ]]; then
      log "Prometheus has $query"
      return 0
    fi
    sleep 2
  done
  die "Prometheus still has no $query (up{job=\"kubernetes-nodes-cadvisor\"}: $(prom_query 'up{job="kubernetes-nodes-cadvisor"}' | jq -c '[.data.result[].value[1]]'))"
}

# prom_query QUERY runs an instant query through the API server's service proxy (no port-forward).
prom_query() {
  k get --raw "/api/v1/namespaces/$PROM_NAMESPACE/services/prometheus-server:80/proxy/api/v1/query?query=$(jq -rn --arg q "$1" '$q | @uri')"
}

run_tests() {
  guard_context
  local l2_default=0
  if h status prometheus --namespace "$PROM_NAMESPACE" >/dev/null 2>&1; then l2_default=1; fi
  # About 15 minutes at most: L2 waits until crossaz has a full 5m step of the sink's series.
  (cd "$ROOT" && E2E_L2="${E2E_L2:-$l2_default}" go test -tags e2e ./hack/e2e/ -count=1 -v -timeout 20m)
}

down() {
  if cluster_exists; then kind delete cluster --name "$CLUSTER"; else log "no kind cluster $CLUSTER"; fi
}

main() {
  require_tools
  case "${1:-all}" in
    up) up ;;
    l2) l2 ;;
    test) run_tests ;;
    down) down ;;
    all)
      up
      l2
      run_tests || die "tests failed; cluster $CLUSTER kept for inspection ('$0 down' removes it)"
      if [[ "${KEEP_CLUSTER:-0}" != 1 ]]; then down; fi
      ;;
    *) die "usage: $0 [up|l2|test|down|all]" ;;
  esac
}

main "$@"
