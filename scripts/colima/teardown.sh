#!/usr/bin/env bash
# teardown.sh
# P3.5 — Removes the Keel Helm release and stops the Colima k8s cluster.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly LOG_FILE='/tmp/keel_colima_teardown.log'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local -r profile="${KEEL_COLIMA_PROFILE:-keel-k8s}"
  log "🧹 Tearing down Keel Colima environment (profile: ${profile})..."
  uninstall_helm "${profile}"
  stop_colima "${profile}"
  log '🎉 Colima k8s environment torn down'
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

function uninstall_helm() {
  local -r profile="${1}"
  local -r context="colima-${profile}"
  log "⎈ Uninstalling keel Helm release (context: ${context})..."
  helm uninstall keel \
    --kube-context "${context}" \
    --namespace keel 2>/dev/null || true
  log '✅ Helm release removed'
}

function stop_colima() {
  local -r profile="${1}"
  log "🛑 Stopping Colima profile: ${profile}..."
  colima stop "${profile}"
  log "✅ Colima profile '${profile}' stopped"
}

main "${@:-}"