#!/usr/bin/env bash
# scripts/lint/vuln.sh
# Dependency vulnerability gate: govulncheck reports only vulnerabilities reachable from keel code.
# Fails on any reachable vulnerability. Install with: make setup-govulncheck

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
  log '🔍 Running govulncheck ./...'
  govulncheck ./...
  log '✅ No reachable vulnerabilities'
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_lint_vuln.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
