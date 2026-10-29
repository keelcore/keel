#!/usr/bin/env bash
# stack-test.sh
# Apple container equivalent of `make test-compose`:
#   1. builds keel:test-max (FLAVOR=max: H3/QUIC) and loads it into the cluster;
#   2. keel-toolbox deploys the k8s stack and compiles the tests/compose suite
#      (scripts/k8s/stack.sh up);
#   3. the unchanged suite runs inside the cluster node, where every test port
#      is a hostPort on all interfaces, wired node -> pod by iptables.
# Requires: scripts/container/setup.sh must have been run first.
# Usage: stack-test.sh [--keep-up]

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

REPO_ROOT=''
KEEP_UP=''
NAME=''
TOOLBOX=''
readonly LOG_FILE='/tmp/keel_container_stack_test.log'
readonly NODE_TEST_BIN='/tmp/compose.test'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  KEEP_UP="${1:-}"
  REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  NAME="${KEEL_CLUSTER_NAME:-keel-k8s}"
  TOOLBOX="${KEEL_TOOLBOX_NAME:-keel-toolbox}"
  trap teardown EXIT
  build_image
  load_image
  stack up
  install_tests
  wait_for_services
  run_tests
  log '🎉 Stack integration tests passed'
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a "${LOG_FILE}" >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || { [ -n "${1:-}" ] && [ "${1}" != '--keep-up' ]; }; then
    log '❌ Error: Unexpected argument (usage: stack-test.sh [--keep-up])'
    exit 1
  fi
}

function stack() {
  container exec -e "KEEL_K8S_CONTEXT=${NAME}" "${TOOLBOX}" bash scripts/k8s/stack.sh "${1}"
}

function teardown() {
  [[ "${KEEP_UP}" == '--keep-up' ]] && { log 'ℹ️  --keep-up set; stack left running'; return; }
  stack down || true
}

function build_image() {
  log '🐳 Building keel:test-max image (FLAVOR=max: H3/QUIC) via Apple container...'
  container build -t 'keel:test-max' --build-arg 'FLAVOR=max' \
    -f "${REPO_ROOT}/scripts/deploy/docker/Dockerfile" \
    "${REPO_ROOT}"
  log '✅ keel:test-max image built'
}

function load_image() {
  log "📤 Loading keel:test-max into cluster '${NAME}'..."
  container cluster load-image --name "${NAME}" 'keel:test-max'
  log '✅ Image loaded'
}

function install_tests() {
  log "📦 Copying dist/compose.test into node '${NAME}'..."
  container exec -i "${NAME}" sh -c "cat > '${NODE_TEST_BIN}' && chmod 0755 '${NODE_TEST_BIN}'" \
    < "${REPO_ROOT}/dist/compose.test"
  log '✅ Test binary installed in node'
}

function in_node_up() {
  container exec "${NAME}" curl -sfk --max-time 2 "${1}" > /dev/null 2>&1
}

function wait_url() {
  local -r url="${1}" label="${2}" max="${3}"
  local i
  i=0
  log "⏳ Polling ${label} at ${url} in node (timeout: ${max}s)..."
  while ! in_node_up "${url}" && (( i < max )); do
    sleep 1
    i=$(( i + 1 ))
  done
  in_node_up "${url}" || { log "❌ Timeout waiting for ${label}"; exit 1; }
  log "✅ ${label} is ready"
}

function wait_for_services() {
  wait_url 'http://127.0.0.1:9000/' 'upstream echo server' '30'
  wait_url 'http://127.0.0.1:9091/healthz' 'keel /healthz' '60'
  wait_url 'https://127.0.0.1:8443/' 'keel HTTPS' '30'
}

function run_tests() {
  log "🧪 Running compose integration tests inside node '${NAME}'..."
  container exec -e 'KEEL_COMPOSE_TESTS=1' "${NAME}" \
    "${NODE_TEST_BIN}" -test.v -test.count=1 -test.timeout 120s \
    2>&1 | tee -a "${LOG_FILE}" >&5
  log '✅ Stack tests complete'
}

main "${@:-}"
