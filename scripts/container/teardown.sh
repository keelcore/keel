#!/usr/bin/env bash
# teardown.sh
# P3.5 — Removes the Keel Helm release, keel-toolbox, and the Apple container
# k8s cluster.
# Env: KEEL_CLUSTER_NAME (default keel-k8s), KEEL_TOOLBOX_NAME (default keel-toolbox).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly LOG_FILE='/tmp/keel_container_teardown.log'

declare -rx KEEL_CLUSTER_NAME="${KEEL_CLUSTER_NAME:-keel-k8s}"
declare -rx KEEL_TOOLBOX_NAME="${KEEL_TOOLBOX_NAME:-keel-toolbox}"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  log "🧹 Tearing down Keel Apple container environment (cluster: ${KEEL_CLUSTER_NAME})..."
  uninstall_helm "${KEEL_CLUSTER_NAME}" "${KEEL_TOOLBOX_NAME}"
  remove_toolbox "${KEEL_TOOLBOX_NAME}"
  delete_cluster "${KEEL_CLUSTER_NAME}"
  log '🎉 Apple container k8s environment torn down'
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

function uninstall_helm() {
  local -r name="${1}" toolbox="${2}"
  log "⎈ Uninstalling keel Helm release (context: ${name})..."
  container exec "${toolbox}" helm uninstall keel \
    --kube-context "${name}" \
    --namespace keel > /dev/null 2>&1 || true
  log '✅ Helm release removed'
}

function remove_toolbox() {
  local -r toolbox="${1}"
  log "🧰 Removing ${toolbox}..."
  container rm -f "${toolbox}" > /dev/null 2>&1 || true
  log "✅ ${toolbox} removed"
}

function delete_cluster() {
  local -r name="${1}"
  log "🛑 Deleting container cluster: ${name}..."
  container cluster delete --name "${name}"
  log "✅ Container cluster '${name}' deleted"
}

main "${@:-}"
