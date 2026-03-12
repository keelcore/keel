#!/usr/bin/env bash
# kind-teardown.sh
# Deletes the keel-ci kind cluster.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly CLUSTER_NAME='keel-ci'
readonly LOG_FILE='/tmp/keel_kind_teardown.log'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  log "🧹 Deleting kind cluster '${CLUSTER_NAME}'..."
  kind delete cluster --name "${CLUSTER_NAME}"
  log "✅ kind cluster '${CLUSTER_NAME}' deleted"
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
