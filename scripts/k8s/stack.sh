#!/usr/bin/env bash
# stack.sh
# Kubernetes equivalent of the docker compose topology for the Apple
# `container` harness. Runs where kubectl reaches the cluster (keel-toolbox).
#   up   — generate certs, load fixtures, deploy tests/compose/k8s-stack.yaml,
#          and compile the tests/compose suite to dist/compose.test (linux,
#          static) for the host to run inside the node.
#   down — delete the stack namespace.
# The stack binds every test port as a hostPort on all node interfaces; the CNI
# portmap plugin wires node -> pod in iptables, so the unchanged compose tests
# reach 127.0.0.1:<port> from inside the node with no proxies.
# Requires: KEEL_K8S_CONTEXT; image keel:test-max loaded into the cluster.
# Usage: stack.sh up|down

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

REPO_ROOT=''
CONTEXT=''
readonly NAMESPACE='keel-stack'
readonly LOG_FILE='/tmp/keel_stack.log'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  CONTEXT="${KEEL_K8S_CONTEXT:?KEEL_K8S_CONTEXT must be set}"
  REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  case "${1}" in
    up)
      generate_certs
      deploy_stack
      build_tests
      ;;
    down)
      delete_stack
      ;;
  esac
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a "${LOG_FILE}" >&5
}

function validate_args() {
  if [ "${#}" -ne 1 ] || { [ "${1:-}" != 'up' ] && [ "${1:-}" != 'down' ]; }; then
    log '❌ Error: usage: stack.sh up|down'
    exit 1
  fi
}

function kc() {
  kubectl --context "${CONTEXT}" -n "${NAMESPACE}" "${@}"
}

function generate_certs() {
  log '🔑 Generating TLS test certificates...'
  bash "${REPO_ROOT}/tests/fixtures/gen-certs.sh"
  log '✅ Certificates ready'
}

function deploy_stack() {
  local -r fx="${REPO_ROOT}/tests/fixtures"
  local deploy
  log "⎈ Deploying stack to namespace ${NAMESPACE} (context: ${CONTEXT})..."
  # A previous teardown deletes asynchronously; never apply into a Terminating namespace.
  kubectl --context "${CONTEXT}" wait --for=delete "namespace/${NAMESPACE}" --timeout=180s \
    > /dev/null 2>&1 || true
  kubectl --context "${CONTEXT}" create namespace "${NAMESPACE}" --dry-run=client -o yaml \
    | kubectl --context "${CONTEXT}" apply -f -
  kc create secret generic keel-fixtures \
    --from-file="keel.yaml=${fx}/keel.yaml" \
    --from-file="keel-secrets.yaml=${fx}/keel-secrets.yaml" \
    --from-file="server.crt=${fx}/certs/server.crt" \
    --from-file="server.key=${fx}/certs/server.key" \
    --dry-run=client -o yaml | kc apply -f -
  kc create configmap otel-config --from-file="otel-collector.yaml=${fx}/otel-collector.yaml" \
    --dry-run=client -o yaml | kc apply -f -
  kc create configmap prometheus-config --from-file="prometheus.yml=${fx}/prometheus.yml" \
    --dry-run=client -o yaml | kc apply -f -
  # Server-side apply: client-side apply merges containerPorts by port number
  # alone, dropping the hostPort of keel's UDP 8443 entry (TCP and UDP share 8443).
  kc apply --server-side -f "${REPO_ROOT}/tests/compose/k8s-stack.yaml"
  for deploy in upstream keel jaeger otel-collector prometheus; do
    kc rollout status "deployment/${deploy}" --timeout=300s || { dump_logs; exit 1; }
  done
  log '✅ Stack deployed'
}

function build_tests() {
  log '🔨 Compiling tests/compose suite to dist/compose.test...'
  mkdir -p "${REPO_ROOT}/dist"
  (cd "${REPO_ROOT}" && CGO_ENABLED=0 go test -c -o dist/compose.test ./tests/compose)
  log '✅ dist/compose.test ready'
}

function dump_logs() {
  log '📋 Stack state at failure:'
  kc get pods -o wide 2>&1 | tee -a "${LOG_FILE}" >&5 || true
  kc logs deployment/keel --tail=200 2>&1 | tee -a "${LOG_FILE}" >&5 || true
}

function delete_stack() {
  log '🧹 Tearing down stack...'
  kubectl --context "${CONTEXT}" delete namespace "${NAMESPACE}" --ignore-not-found --wait=false 2>&1 \
    | tee -a "${LOG_FILE}" >&5
  log '✅ Stack removed'
}

main "${@:-}"
