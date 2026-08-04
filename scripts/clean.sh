#!/usr/bin/env bash
# clean.sh
# Canonical entrypoint for `make clean`. Delegates to
# scripts/clean.local.sh if present, allowing each consumer to define
# project-specific artifact removal (rm -rf dist/, target/, node_modules/,
# etc.) without modifying the canonical script. If no local recipe
# exists, prints a guidance message and exits 0.

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
  if [ -f scripts/clean.local.sh ]; then
    log '🧹 Delegating to scripts/clean.local.sh'
    bash scripts/clean.local.sh
    return 0
  fi
  log '✅ Nothing to clean (create scripts/clean.local.sh to customize)'
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/clean.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
