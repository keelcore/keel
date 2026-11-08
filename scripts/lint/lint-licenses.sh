#!/usr/bin/env bash
# scripts/lint/lint-licenses.sh
# Dependency licenses in every repo-owned Go module. Allow list: config/go-licenses.txt, adjusted by
# config/go-licenses.local.txt.
# Project hook: lint-licenses.local.sh runs first, when present, with this script's arguments
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

# shellcheck source=../lib/paths.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"
# shellcheck source=../lib/tool-config.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/tool-config.sh"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  local allowed mod found=0
  allowed="$(config_list go-licenses | paste -sd, -)"
  while IFS= read -r mod; do
    [ -n "${mod}" ] || continue
    found=1
    log "🔍 go-licenses in $(dirname "${mod}") (allowed: ${allowed})"
    check_module "$(dirname "${mod}")" "${allowed}"
  done < <(source_files '*go.mod')
  if [ "${found}" -eq 0 ]; then
    log 'ℹ️  No Go module; nothing to check'
    return 0
  fi
  log '✅ All module licenses allowed'
}

# check_module DIR ALLOWED: the module's dependencies; its own packages carry the repository's license.
function check_module() {
  local -r dir="${1}" allowed="${2}"
  local self
  self="$(awk '$1 == "module" { print $2; exit }' "${dir}/go.mod")"
  (cd "${dir}" && go-licenses check ./... --allowed_licenses="${allowed}" --ignore="${self}")
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/lint_licenses.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
