#!/usr/bin/env bash
# scripts/lint/licenses.sh
# License gate: every Go module linked into keel must carry a license on ALLOWED_LICENSES.
# Install with: make setup-go-licenses

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly ALLOWED_LICENSES='Apache-2.0,BSD-2-Clause,BSD-3-Clause,MIT,ISC'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  log "🔍 go-licenses check (allowed: ${ALLOWED_LICENSES})"
  go-licenses check ./... --allowed_licenses="${ALLOWED_LICENSES}"
  log '✅ All module licenses allowed'
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_lint_licenses.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
