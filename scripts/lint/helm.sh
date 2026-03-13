#!/usr/bin/env bash
# helm.sh
# Lint the Keel Helm chart (pure Helm validation, no cluster required).
# Runnable locally and in CI identically.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly CHART_DIR='helm/keel'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  require_helm
  log "Linting ${CHART_DIR}"
  run_lint
  log "Helm lint passed"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_lint_helm.log' >&5
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

function run_lint() {
  helm lint "${CHART_DIR}" \
    --set 'mode=sidecar' \
    --set 'sidecar.app.image=placeholder'
}

main "${@:-}"
