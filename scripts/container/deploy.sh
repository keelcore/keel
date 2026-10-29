#!/usr/bin/env bash
# deploy.sh
# P3.5 — Builds keel:test with Apple's `container` CLI, loads it into the
# container cluster, and deploys it via Helm from keel-toolbox.
# Requires: scripts/container/setup.sh must have been run first.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

REPO_ROOT=''
readonly LOG_FILE='/tmp/keel_container_deploy.log'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  local -r name="${KEEL_CLUSTER_NAME:-keel-k8s}"
  local -r toolbox="${KEEL_TOOLBOX_NAME:-keel-toolbox}"
  log "⎈ Deploying Keel to container cluster: ${name}..."
  require_running "${name}"
  require_running "${toolbox}"
  build_image
  load_image "${name}"
  deploy_helm "${name}" "${toolbox}"
  log '🎉 Keel deployed successfully'
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

function require_running() {
  local -r id="${1}"
  container ls | awk -v id="${id}" 'NR > 1 && $1 == id { found = 1 } END { exit !found }' || {
    log "❌ '${id}' is not running. Run: scripts/container/setup.sh"
    exit 1
  }
  log "✅ ${id} is running"
}

function build_image() {
  KEEL_IMAGE_TOOL=container bash "${REPO_ROOT}/scripts/build/certs-image.sh"
  log '🐳 Building keel:test image via Apple container...'
  container build -t 'keel:test' --build-arg 'FLAVOR=min' \
    -f "${REPO_ROOT}/scripts/deploy/docker/Dockerfile" \
    "${REPO_ROOT}"
  log '✅ keel:test image built'
}

function load_image() {
  local -r name="${1}"
  log "📤 Loading keel:test into cluster '${name}'..."
  container cluster load-image --name "${name}" 'keel:test'
  log '✅ Image loaded'
}

function deploy_helm() {
  local -r name="${1}" toolbox="${2}"
  log "⎈ Delegating to helm-deploy.sh in ${toolbox} (context: ${name})..."
  container exec -e "KEEL_K8S_CONTEXT=${name}" "${toolbox}" \
    bash scripts/k8s/helm-deploy.sh
}

main "${@:-}"
