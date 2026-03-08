#!/usr/bin/env bash
# compose.sh
# P3 Docker Compose integration test orchestrator.
# Builds keel:test, starts the compose stack, waits for readiness,
# runs KEEL_COMPOSE_TESTS=1 go test ./tests/compose/..., then tears down.
# Usage: KEEL_COMPOSE_TESTS=1 scripts/test/compose.sh [--keep-up]

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

REPO_ROOT=''
COMPOSE_FILE=''
KEEP_UP=''
readonly LOG_FILE='/tmp/keel_compose_test.log'
readonly KEEL_HEALTH_URL='http://127.0.0.1:9091/healthz'
readonly KEEL_HTTPS_URL='https://127.0.0.1:8443/healthz'
readonly UPSTREAM_URL='http://127.0.0.1:9000/'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  KEEP_UP="${1:-}"
  REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  COMPOSE_FILE="${REPO_ROOT}/docker-compose.test.yaml"
  trap teardown EXIT
  generate_certs
  build_and_start
  wait_for_services
  run_compose_tests
  log '🎉 Compose integration tests passed'
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a "${LOG_FILE}" >&5
}

function validate_args() {
  if [ "${#}" -gt 0 ] && [ -z "${1:-}" ]; then
    log '❌ Error: Unexpected empty argument'
    exit 1
  fi
}

function teardown() {
  [[ "${KEEP_UP}" == '--keep-up' ]] && { log 'ℹ️  --keep-up set; compose stack left running'; return; }
  log '🧹 Tearing down compose stack...'
  docker compose -f "${COMPOSE_FILE}" down --volumes --remove-orphans \
    2>&1 | tee -a "${LOG_FILE}" >&5 || true
  log '✅ Compose stack removed'
}

function generate_certs() {
  log '🔑 Generating TLS test certificates...'
  bash "${REPO_ROOT}/tests/fixtures/gen-certs.sh"
  log '✅ Certificates ready'
}

function build_and_start() {
  log '🐳 Building keel:test image and starting compose stack...'
  docker compose -f "${COMPOSE_FILE}" up -d --build \
    2>&1 | tee -a "${LOG_FILE}" >&5
  log '✅ Compose stack started'
}

function wait_for_services() {
  log '⏳ Waiting for services to be ready...'
  wait_http "${UPSTREAM_URL}" 'upstream echo server' '30'
  wait_http "${KEEL_HEALTH_URL}" 'keel /healthz' '60'
  wait_https "${KEEL_HTTPS_URL}" 'keel HTTPS /healthz' '30'
  log '✅ All services ready'
}

function is_up() {
  curl -sf --max-time 2 "${1}" > /dev/null 2>&1
}

function is_up_https() {
  curl -sfk --max-time 2 "${1}" > /dev/null 2>&1
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

function wait_https() {
  local -r url="${1}" label="${2}" max="${3:-60}"
  local i
  i=0
  log "⏳ Polling ${label} at ${url} (timeout: ${max}s)..."
  while ! is_up_https "${url}" && (( i < max )); do
    sleep 1
    i=$(( i + 1 ))
  done
  is_up_https "${url}" || { log "❌ Timeout waiting for ${label}"; exit 1; }
  log "✅ ${label} is ready"
}

function run_compose_tests() {
  log '🧪 Running compose integration tests...'
  # KEEL_COMPOSE_TESTS must be set by the caller (compose.sh --keep-up or CI job env).
  if [ -z "${KEEL_COMPOSE_TESTS:-}" ]; then
    log '❌ KEEL_COMPOSE_TESTS is not set — compose tests would all be skipped'
    exit 1
  fi
  go test -v -count=1 -timeout 120s ./tests/compose/... \
    2>&1 | tee -a "${LOG_FILE}" >&5
  log '✅ Compose tests complete'
}

main "${@:-}"