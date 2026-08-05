#!/usr/bin/env bash
# scripts/test/gen-certs.sh
# Generate self-signed TLS certificates for local/integration testing. Wraps the
# fixture generator at tests/fixtures/gen-certs.sh, which is also invoked
# directly by the BATS and compose suites, so the fixture stays where it is.

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
  log '🔑 Generating self-signed test certificates...'
  bash tests/fixtures/gen-certs.sh
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_gen_certs.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
