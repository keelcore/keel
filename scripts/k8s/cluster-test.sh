#!/usr/bin/env bash
# cluster-test.sh
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

# Port-forward PID — set in start_port_forward, cleared in stop_port_forward.
PORT_FORWARD_PID=''
readonly LOG_FILE='/tmp/keel_cluster_test.log'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local -r context="${KEEL_K8S_CONTEXT:?KEEL_K8S_CONTEXT must be set}"
  trap 'stop_port_forward' EXIT
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
  log '🔍 Testing probe endpoints via port-forward...'
  start_port_forward "${context}"
  wait_http 'http://127.0.0.1:19091/healthz' 'health probe' '30'
  wait_http 'http://127.0.0.1:19092/readyz'  'ready probe'  '30'
  stop_port_forward
  log '✅ Probe endpoints responded correctly'
}

function start_port_forward() {
  local -r context="${1}"
  log '🔌 Starting port-forward: 19091→9091, 19092→9092...'
  kubectl_cmd --context "${context}" -n keel port-forward \
    svc/keel 19091:9091 19092:9092 &
  PORT_FORWARD_PID=$!
  sleep 2
  log "✅ Port-forward running (PID: ${PORT_FORWARD_PID})"
}

function stop_port_forward() {
  [[ -z "${PORT_FORWARD_PID}" ]] && return 0
  log '🔌 Stopping port-forward...'
  kill "${PORT_FORWARD_PID}" 2>/dev/null || true
  PORT_FORWARD_PID=''
}

function is_up() {
  curl -sf --max-time 2 "${1}" > /dev/null 2>&1
}

function wait_http() {
  local -r url="${1}" label="${2}" max="${3:-60}"
  local i
  i=0
  log "⏳ Polling ${label} at ${url} (timeout: ${max}s)..."
  while ! is_up "${url}" && (( i < max )); do
    sleep 1
    i=$(( i + 1 ))
  done
  is_up "${url}" || { log "❌ Timeout waiting for ${label}"; exit 1; }
  log "✅ ${label} is ready"
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
