#!/usr/bin/env bash
# test.sh
# P3.5 — Runs k8s-specific integration tests against the Apple container
# cluster. Delegates to scripts/k8s/cluster-test.sh inside keel-toolbox.
# Requires: scripts/container/deploy.sh must have been run first.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly LOG_FILE='/tmp/keel_container_test.log'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local -r name="${KEEL_CLUSTER_NAME:-keel-k8s}"
  local -r toolbox="${KEEL_TOOLBOX_NAME:-keel-toolbox}"
  log "🧪 Delegating to cluster-test.sh in ${toolbox} (context: ${name})..."
  container exec -e "KEEL_K8S_CONTEXT=${name}" "${toolbox}" \
    bash scripts/k8s/cluster-test.sh
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

main "${@:-}"
