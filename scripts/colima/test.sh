#!/usr/bin/env bash
# test.sh
# P3.5 — Runs k8s-specific integration tests against the Colima cluster.
# Delegates to scripts/k8s/cluster-test.sh.
# Requires: scripts/colima/deploy.sh must have been run first.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly LOG_FILE='/tmp/keel_colima_test.log'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local -r profile="${KEEL_COLIMA_PROFILE:-keel-k8s}"
  local repo_root
  repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  KEEL_K8S_CONTEXT="${KEEL_K8S_CONTEXT:-colima-${profile}}"
  export KEEL_K8S_CONTEXT
  log "🧪 Delegating to cluster-test.sh (context: ${KEEL_K8S_CONTEXT})..."
  "${repo_root}/scripts/k8s/cluster-test.sh"
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

main "${@:-}"
