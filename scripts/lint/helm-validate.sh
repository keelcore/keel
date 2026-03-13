#!/usr/bin/env bash
# helm-validate.sh
# Render the Keel Helm chart and validate rendered objects against the
# Kubernetes JSON schema (minimum supported version).  No live cluster
# required — kubeconform downloads schemas from the public schema registry.
# Runnable locally and in CI identically.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly CHART_DIR='helm/keel'
# Minimum Kubernetes version whose API schema the chart must satisfy.
readonly KUBE_MIN_VERSION='1.28.0'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  require_helm
  require_kubeconform
  log "Validating rendered template against Kubernetes ${KUBE_MIN_VERSION} schema"
  run_validate
  log "Helm schema validation passed"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_lint_helm_validate.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected arg'
    exit 1
  fi
}

function require_helm() {
  if ! command -v helm >/dev/null 2>&1; then
    log "ERROR: helm not found in PATH"
    log "  Install via: scripts/ci/setup-helm.sh"
    exit 1
  fi
}

function require_kubeconform() {
  if ! command -v kubeconform >/dev/null 2>&1; then
    log "ERROR: kubeconform not found in PATH"
    log "  Install via: scripts/ci/setup-kubeconform.sh"
    exit 1
  fi
}

function run_validate() {
  helm template keel-test "${CHART_DIR}" \
    --set 'mode=sidecar' \
    --set 'sidecar.app.image=placeholder' \
    | kubeconform \
        -kubernetes-version "${KUBE_MIN_VERSION}" \
        -strict \
        -summary \
        -output pretty
}

main "${@:-}"
