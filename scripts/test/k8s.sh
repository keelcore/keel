#!/usr/bin/env bash
# k8s.sh
# CI/local entrypoint for kind-based k8s integration tests.
# Orchestrates: kind-setup → kind-load → helm-deploy → cluster-test.
# Exports KEEL_K8S_CONTEXT and KEEL_K8S_KUBECONFIG for downstream scripts.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly LOG_FILE='/tmp/keel_test_k8s.log'
REPO_ROOT=''

export KEEL_K8S_CONTEXT='kind-keel-ci'
export KEEL_K8S_KUBECONFIG='/tmp/keel-kind.kubeconfig'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  trap 'teardown' EXIT
  log '⎈ Starting kind k8s integration tests...'
  bash "${REPO_ROOT}/scripts/k8s/kind-setup.sh"
  bash "${REPO_ROOT}/scripts/k8s/kind-load.sh"
  bash "${REPO_ROOT}/scripts/k8s/helm-deploy.sh"
  bash "${REPO_ROOT}/scripts/k8s/cluster-test.sh"
  log '🎉 All k8s integration tests passed'
}

function teardown() {
  bash "${REPO_ROOT}/scripts/k8s/kind-teardown.sh"
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
