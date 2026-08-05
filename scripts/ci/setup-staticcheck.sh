#!/usr/bin/env bash
# scripts/ci/setup-staticcheck.sh
# Install the staticcheck linter into the Go toolchain bin. No-op if present.

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
  if command -v staticcheck >/dev/null 2>&1; then
    log "✅ staticcheck already installed ($(command -v staticcheck))"
    return 0
  fi
  log '🔧 Installing staticcheck...'
  go install honnef.co/go/tools/cmd/staticcheck@latest
  log '✅ staticcheck installed'
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_setup_staticcheck.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
