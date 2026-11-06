#!/usr/bin/env bash
# scripts/lint/vex.sh
# Validate dist/<project>.openvex.json against config/vex-checks.jq.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/tool-config.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/tool-config.sh"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local -r document="dist/$(project_name).openvex.json"
  local problems
  # jq program: prints one line per problem; empty output means the document is valid.
  problems="$(jq -r -f "$(config_default_dir)/vex-checks.jq" "${document}")"
  if [ -n "${problems}" ]; then
    log "❌ ${document}:"
    log "${problems}"
    exit 1
  fi
  log "✅ ${document} valid ($(jq '.statements | length' "${document}") statement(s))"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/lint_vex.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
