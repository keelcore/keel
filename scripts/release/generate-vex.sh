#!/usr/bin/env bash
# scripts/release/generate-vex.sh
# OpenVEX from govulncheck reachability, as dist/<project>.openvex.json.
# Project hook: generate-vex.local.sh runs first, when present, with this script's arguments
# (scripts/lib/local-hook.sh: the same path, in the repository this runs in).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/local-hook.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/local-hook.sh"

# shellcheck source=../lib/tool-config.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/tool-config.sh"

declare OUT_DIR='dist'

function main() {
  exec 5>&1
  if [ "${1:-}" = '--out-dir' ]; then
    OUT_DIR="${2}"
    shift 2
  fi
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  mkdir -p "${OUT_DIR}"
  local -r document="${OUT_DIR}/$(project_name).openvex.json"
  log "Generating OpenVEX → ${document}"
  govulncheck -format openvex ./... > "${document}"
  log "✅ OpenVEX: $(jq '.statements | length' "${document}") statement(s)"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/release_vex.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
