#!/usr/bin/env bash
# scripts/lint/gosec.sh
# Go SAST: gosec in every repo-owned Go module. Excluded rules: config/gosec.local.txt.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/paths.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"
# shellcheck source=../lib/tool-config.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/tool-config.sh"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local -a exclude=()
  local rules
  rules="$(config_list gosec | paste -sd, -)"
  [ -z "${rules}" ] || exclude=("-exclude=${rules}")
  local mod found=0
  while IFS= read -r mod; do
    [ -n "${mod}" ] || continue
    found=1
    log "🔍 gosec in $(dirname "${mod}")${rules:+ (excluding ${rules})}"
    (cd "$(dirname "${mod}")" && gosec -quiet ${exclude[@]+"${exclude[@]}"} -exclude-dir=vendor ./...)
  done < <(source_files '*go.mod')
  if [ "${found}" -eq 0 ]; then
    log 'ℹ️  No Go module; nothing to check'
    return 0
  fi
  log '✅ gosec clean'
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/lint_gosec.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
