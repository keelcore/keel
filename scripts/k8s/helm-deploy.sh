#!/usr/bin/env bash
# helm-deploy.sh
# Cluster-agnostic Helm deployment for keel.
# Reads KEEL_K8S_CONTEXT (required) and KEEL_K8S_KUBECONFIG (optional).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly LOG_FILE='/tmp/keel_helm_deploy.log'
REPO_ROOT=''

function main() {
  exec 5>&1
  validate_args "${@:-}"
  REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  local -r context="${KEEL_K8S_CONTEXT:?KEEL_K8S_CONTEXT must be set}"
  local -r values="${REPO_ROOT}/tests/fixtures/colima/values.yaml"
  local -r chart="${REPO_ROOT}/helm/keel"
  log "⎈ Deploying keel via Helm (context: ${context})..."
  run_helm "${context}" "${values}" "${chart}"
  log '✅ Helm release deployed'
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

function run_helm() {
  local -r context="${1}" values="${2}" chart="${3}"
  if [[ -n "${KEEL_K8S_KUBECONFIG:-}" ]]; then
    helm upgrade --install keel "${chart}" \
      --kubeconfig "${KEEL_K8S_KUBECONFIG}" \
      --kube-context "${context}" \
      --namespace keel \
      --create-namespace \
      --values "${values}" \
      --wait \
      --timeout 120s
  else
    helm upgrade --install keel "${chart}" \
      --kube-context "${context}" \
      --namespace keel \
      --create-namespace \
      --values "${values}" \
      --wait \
      --timeout 120s
  fi
}

main "${@:-}"
