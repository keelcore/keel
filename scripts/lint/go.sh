#!/usr/bin/env bash
# go.sh
# Run Go linting: vet + staticcheck.
# Runnable locally and in CI identically.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/paths.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  log "Running go vet"
  run_vet
  log "Running staticcheck"
  run_staticcheck
  log "Lint passed"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_lint_go.log' >&5
}

function validate_args() { :; }

function run_vet() {
  go_pkgs | xargs go vet
}

function run_staticcheck() {
  if ! command -v staticcheck >/dev/null 2>&1; then
    log "staticcheck not found; install with: go install honnef.co/go/tools/cmd/staticcheck@latest"
    log "Skipping staticcheck"
    return 0
  fi
  go_pkgs | xargs staticcheck
}

main "${@:-}"
