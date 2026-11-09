#!/usr/bin/env bash
# deploy.sh
# P3.5 — Builds keel:test with Apple's `container` CLI, which the cluster's nodes pull from
# the local image store, and deploys it via Helm from keel-toolbox.
# Requires: scripts/container/setup.sh must have been run first.
# Env: KEEL_CLUSTER_NAME (default keel-k8s), KEEL_TOOLBOX_NAME (default keel-toolbox).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

REPO_ROOT=''
readonly LOG_FILE='/tmp/keel_container_deploy.log'

declare -rx KEEL_CLUSTER_NAME="${KEEL_CLUSTER_NAME:-keel-k8s}"
declare -rx KEEL_TOOLBOX_NAME="${KEEL_TOOLBOX_NAME:-keel-toolbox}"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  log "⎈ Deploying Keel to container cluster: ${KEEL_CLUSTER_NAME}..."
  require_running "${KEEL_CLUSTER_NAME}"
  require_running "${KEEL_TOOLBOX_NAME}"
  build_image
  deploy_helm "${KEEL_CLUSTER_NAME}" "${KEEL_TOOLBOX_NAME}"
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
  container inspect "${id}" 2> /dev/null | jq -e '.[0].status.state == "running"' > /dev/null || {
    log "❌ '${id}' is not running. Run: scripts/container/setup.sh"
    exit 1
  }
  log "✅ ${id} is running"
}

function build_image() {
  KEEL_IMAGE_TOOL=container bash "${REPO_ROOT}/scripts/build/build-certs-image.sh"
  log '🐳 Building keel:test image via Apple container...'
  container build -t 'keel:test' --build-arg 'FLAVOR=min' \
    -f "${REPO_ROOT}/scripts/deploy/docker/Dockerfile" \
    "${REPO_ROOT}"
  log '✅ keel:test image built'
}

function deploy_helm() {
  local -r name="${1}" toolbox="${2}"
  log "⎈ Delegating to deploy-k8s-helm.sh in ${toolbox} (context: ${name})..."
  container exec -e "KEEL_K8S_CONTEXT=${name}" "${toolbox}" \
    bash scripts/k8s/deploy-k8s-helm.sh
}

main "${@:-}"
