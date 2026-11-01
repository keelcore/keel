#!/usr/bin/env bash
# scripts/lint/gosec.sh
# Go security linter (SAST). Fails on any finding. Install with: make setup-gosec
#
# Excluded rules:
#   G304 (file inclusion via variable) — every os.ReadFile path in keel comes from
#        operator configuration (keel.yaml, TLS and key files), never from request input.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly EXCLUDED_RULES='G304'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  log "🔍 Running gosec (excluding ${EXCLUDED_RULES})"
  gosec -quiet -exclude="${EXCLUDED_RULES}" -exclude-dir=vendor ./...
  log '✅ gosec clean'
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_lint_gosec.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
