#!/usr/bin/env bash
# helm.sh
# Lint the Keel Helm chart and validate the rendered template.
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
  log "Validating rendered template"
  run_template_validate
  log "Helm lint passed"
}

function log() {
  printf '%s\n' "${1:-}" >&5
}

function validate_args() { :; }

function require_helm() {
  if ! command -v helm >/dev/null 2>&1; then
    printf 'ERROR: helm not found in PATH\n' >&2
    printf '  Install via: scripts/ci/setup-helm.sh\n' >&2
    printf '  Or see: https://helm.sh/docs/intro/install/\n' >&2
    exit 1
  fi
}

function run_lint() {
  helm lint "${CHART_DIR}"
}

function run_template_validate() {
  if command -v kubectl >/dev/null 2>&1; then
    helm template keel-test "${CHART_DIR}" | kubectl apply --dry-run=client -f -
  else
    log "kubectl not found; skipping dry-run apply (helm lint still ran)"
  fi
}

main "${@:-}"