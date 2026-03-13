#!/usr/bin/env bash
# consistency.sh
# Runs the BATS consistency suite that verifies the Go config struct,
# Helm chart values, configmap template, and config-reference.md are in sync.
# Requires: helm, bats-core in PATH.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

function main() {
  exec 5>&1
  validate_args "${@:-}"
  log "Running config consistency checks"
  ensure_helm
  ensure_bats
  run_suite
  log "All consistency checks passed"
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_consistency.log' >&5
}

function validate_args() { :; }

function ensure_helm() {
  if ! command -v helm >/dev/null 2>&1; then
    log "ERROR: helm not found in PATH — install helm >= 3.8"
    exit 1
  fi
}

function ensure_bats() {
  if ! command -v bats >/dev/null 2>&1; then
    log "bats not found; installing via scripts/ci/setup-bats.sh"
    bash scripts/ci/setup-bats.sh
  fi
}

function run_suite() {
  bats tests/consistency.bats
}

main "${@:-}"
