#!/usr/bin/env bash
# build.sh
# Canonical entrypoint for `make build`. Delegates to
# scripts/build.local.sh if present, allowing each consumer to define a
# project-specific build (Go, Rust, Docker image, etc.) without modifying
# the canonical script. If no local recipe exists, prints a guidance
# message and exits 0.

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
  if [ -f scripts/build.local.sh ]; then
    log '🔨 Delegating to scripts/build.local.sh'
    bash scripts/build.local.sh
    return 0
  fi
  log '✅ No build configured (create scripts/build.local.sh to customize)'
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/build.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
