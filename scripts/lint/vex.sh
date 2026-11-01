#!/usr/bin/env bash
# scripts/lint/vex.sh
# Validates the OpenVEX document (security/keel.openvex.json) that the release
# attests to every image index: OpenVEX v0.2.0 context, UTC timestamps, and per
# statement a vulnerability name, products, a valid status, a justification or
# impact statement for not_affected, and an action statement for affected.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly VEX_DOCUMENT='security/keel.openvex.json'
# jq program: prints one line per problem; empty output means the document is valid.
readonly VEX_CHECKS='scripts/lint/vex-checks.jq'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local problems
  problems="$(jq -r -f "${VEX_CHECKS}" "${VEX_DOCUMENT}")"
  if [ -n "${problems}" ]; then
    log "❌ ${VEX_DOCUMENT}:"
    log "${problems}"
    exit 1
  fi
  log "✅ ${VEX_DOCUMENT} valid ($(jq '.statements | length' "${VEX_DOCUMENT}") statement(s))"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_lint_vex.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
