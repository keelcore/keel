#!/usr/bin/env bash
# integration-test.sh
# Canonical entrypoint for `make integration-test`. Delegates to
# scripts/integration-test.local.sh if present, allowing each consumer to
# define end-to-end / BATS / cross-service test invocation without
# modifying the canonical script. If no local recipe exists, prints a
# guidance message and exits 0.

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
  if [ -f scripts/integration-test.local.sh ]; then
    log '🧪 Delegating to scripts/integration-test.local.sh'
    bash scripts/integration-test.local.sh
    return 0
  fi
  log '✅ No integration tests configured (create scripts/integration-test.local.sh to customize)'
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/integration-test.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
