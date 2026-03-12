#!/usr/bin/env bash
# deploy.sh
# P3.5 — Builds the keel:test image inside the Colima VM and deploys it
# via Helm to the local k8s cluster.
# Requires: colima setup.sh must have been run first.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

REPO_ROOT=''
readonly LOG_FILE='/tmp/keel_colima_deploy.log'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  local -r profile="${KEEL_COLIMA_PROFILE:-keel-k8s}"
  log "⎈ Deploying Keel to Colima k8s profile: ${profile}..."
  check_colima_running "${profile}"
  build_image "${profile}"
  deploy_helm "${profile}"
  log '🎉 Keel deployed successfully'
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

function check_colima_running() {
  local -r profile="${1}"
  log "🔍 Checking Colima status for profile: ${profile}..."
  colima status "${profile}" > /dev/null 2>&1 || {
    log "❌ Colima profile '${profile}' is not running."
    log '   Run: scripts/colima/setup.sh'
    exit 1
  }
  log "✅ Colima profile '${profile}' is running"
}

function build_image() {
  local -r profile="${1}"
  log '🐳 Building keel:test image inside Colima VM via nerdctl...'
  colima nerdctl --profile "${profile}" -- \
    build -t 'keel:test' --build-arg 'FLAVOR=min' "${REPO_ROOT}"
  log '✅ keel:test image built'
}

function deploy_helm() {
  local -r profile="${1}"
  KEEL_K8S_CONTEXT="${KEEL_K8S_CONTEXT:-colima-${profile}}"
  export KEEL_K8S_CONTEXT
  log "⎈ Delegating to helm-deploy.sh (context: ${KEEL_K8S_CONTEXT})..."
  "${REPO_ROOT}/scripts/k8s/helm-deploy.sh"
}

main "${@:-}"