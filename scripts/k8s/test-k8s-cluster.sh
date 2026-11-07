#!/usr/bin/env bash
# test-k8s-cluster.sh
# Cluster-agnostic k8s integration tests for keel.
# Tests: pod readiness, probe endpoints, graceful rolling restart.
# Reads KEEL_K8S_CONTEXT (required) and KEEL_K8S_KUBECONFIG (optional).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly LOG_FILE='/tmp/keel_cluster_test.log'
# Probe client: an ephemeral container in keel's pod shares its network
# namespace, so busybox wget reaches 127.0.0.1:<probe port> with no proxy in
# the path. Same pinned image as scripts/deploy/docker/Dockerfile.certs.
readonly PROBE_CONTAINER="keel-probe-$$"  # ephemeral containers stay in the pod: one name per run
readonly PROBE_IMAGE='alpine:3.22@sha256:bfe7ceb9f09ec0566c48fb93356e45a47a00be5a1dd14fa5b909e7709c66b034'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local -r context="${KEEL_K8S_CONTEXT:?KEEL_K8S_CONTEXT must be set}"
  log "🧪 Running Keel k8s integration tests (context: ${context})..."
  test_pod_readiness "${context}"
  test_probe_endpoints "${context}"
  test_graceful_shutdown "${context}"
  log '🎉 All k8s integration tests passed'
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a "${LOG_FILE}" >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

function kubectl_cmd() {
  if [[ -n "${KEEL_K8S_KUBECONFIG:-}" ]]; then
    kubectl --kubeconfig "${KEEL_K8S_KUBECONFIG}" "${@}"
  else
    kubectl "${@}"
  fi
}

function test_pod_readiness() {
  local -r context="${1}"
  log '📋 Waiting for Keel pod to be Ready...'
  kubectl_cmd --context "${context}" -n keel wait \
    --for=condition=Ready pod \
    --selector 'app.kubernetes.io/name=keel' \
    --timeout=120s
  log '✅ Keel pod is Ready'
}

function test_probe_endpoints() {
  local -r context="${1}"
  local pod
  pod="$(kubectl_cmd --context "${context}" -n keel get pod \
    --selector 'app.kubernetes.io/name=keel' -o jsonpath='{.items[0].metadata.name}')"
  log "🔍 Testing probe endpoints from inside pod ${pod}..."
  start_probe_container "${context}" "${pod}"
  expect_probe "${context}" "${pod}" 'http://127.0.0.1:9091/healthz' 'ok'
  expect_probe "${context}" "${pod}" 'http://127.0.0.1:9092/readyz' 'ready'
  log '✅ Probe endpoints responded correctly'
}

# start_probe_container: an ephemeral container (PROBE_CONTAINER) in pod that stays up, ready for
# exec. Not `kubectl debug --stdin -- wget`: debug attaches after the container starts, and a wget
# that finishes first loses its output (empty body, exit 0).
function start_probe_container() {
  local -r context="${1}" pod="${2}"
  local i state
  kubectl_cmd --context "${context}" -n keel debug "pod/${pod}" \
    --image "${PROBE_IMAGE}" --profile general --container "${PROBE_CONTAINER}" --quiet \
    -- sleep 600 > /dev/null
  for i in $(seq 1 60); do
    state="$(kubectl_cmd --context "${context}" -n keel get pod "${pod}" -o \
      jsonpath="{.status.ephemeralContainerStatuses[?(@.name==\"${PROBE_CONTAINER}\")].state.running.startedAt}")"
    [ -n "${state}" ] && return 0
    sleep 1
  done
  log "❌ probe container ${PROBE_CONTAINER} not running in ${pod} after ${i}s"
  exit 1
}

# expect_probe: GET url from the probe container in pod; the body must contain want (wget exits
# non-zero on any non-2xx status).
function expect_probe() {
  local -r context="${1}" pod="${2}" url="${3}" want="${4}"
  local body
  body="$(kubectl_cmd --context "${context}" -n keel exec "${pod}" -c "${PROBE_CONTAINER}" \
    -- wget -q -T 5 -O - "${url}" < /dev/null)" \
    || { log "❌ ${url} failed"; exit 1; }
  [[ "${body}" == *"${want}"* ]] || { log "❌ ${url} body '${body}', want '${want}'"; exit 1; }
  log "✅ ${url} → ${want}"
}

function test_graceful_shutdown() {
  local -r context="${1}"
  log '🔄 Testing graceful pod rolling restart...'
  kubectl_cmd --context "${context}" -n keel rollout restart deployment/keel
  kubectl_cmd --context "${context}" -n keel rollout status deployment/keel \
    --timeout=120s
  log '✅ Rolling restart completed without errors'
}

main "${@:-}"
