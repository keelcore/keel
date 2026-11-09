#!/usr/bin/env bash
# check-legal-drift.sh
# Verifies that every legal-file copy the repository lists in config/legal-copies.local.txt
# (scripts/lib/legal-copies.sh) is identical to its source. Changes nothing: a missing or differing copy
# fails, naming `make ensure-legal-notices`, which writes it. No list, nothing to check. Runs in `make audit`
# and in the commit hook.
# Environment: CI (default empty; `true` says to sync locally), GITHUB_STEP_SUMMARY (default empty; when set,
# failures are appended to it).
# Project hook: check-legal-drift.local.sh runs first, when present, with this script's arguments
# (scripts/lib/local-hook.sh: the same path, in the repository this runs in).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=./lib/local-hook.sh
source "$(dirname "${BASH_SOURCE[0]}")/./lib/local-hook.sh"
# shellcheck source=./lib/legal-copies.sh
source "$(dirname "${BASH_SOURCE[0]}")/./lib/legal-copies.sh"

declare -rx CI="${CI:-}"
declare -rx GITHUB_STEP_SUMMARY="${GITHUB_STEP_SUMMARY:-}"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  cd "$(git rev-parse --show-toplevel)"
  local pairs
  pairs="$(legal_copies)"
  if [ -z "${pairs}" ]; then
    log "✅ No legal copies listed in ${LEGAL_COPIES_FILE}: nothing to check"
    return 0
  fi
  local src dst findings='' sources=0 copies=0
  while IFS=$'\t' read -r src dst; do
    if [ ! -f "${src}" ]; then
      findings+="${src} → ${dst}: source missing"$'\n'
      sources=1
    elif [ ! -f "${dst}" ]; then
      findings+="${src} → ${dst}: copy missing"$'\n'
      copies=1
    elif ! cmp -s "${src}" "${dst}"; then
      findings+="${src} → ${dst}: copy differs from its source"$'\n'
      copies=1
    fi
  done <<< "${pairs}"
  if [ -z "${findings}" ]; then
    log '✅ Every legal copy matches its source'
    return 0
  fi
  report "${findings}" "${sources}" "${copies}"
  return 1
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/check-legal-drift.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 0 ] && [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected arg'
    exit 1
  fi
}

# report FINDINGS SOURCES COPIES: logs each finding and its fix; SOURCES/COPIES are 1 when a source or a
# copy is at fault.
function report() {
  local -r findings="${1}" sources="${2}" copies="${3}"
  local fixes='' line
  [ "${sources}" -eq 0 ] || fixes+="Restore the source, or correct ${LEGAL_COPIES_FILE}."$'\n'
  if [ "${copies}" -eq 1 ] && [ "${CI}" = 'true' ]; then
    fixes+='Run `make ensure-legal-notices` locally and commit the copies.'$'\n'
  elif [ "${copies}" -eq 1 ]; then
    fixes+='Fix: `make ensure-legal-notices`, then commit the copies.'$'\n'
  fi
  log '❌ Legal file drift detected:'
  while IFS= read -r line; do
    log "  ${line}"
  done <<< "${findings}${fixes%$'\n'}"
  [ -z "${GITHUB_STEP_SUMMARY}" ] || summarize "${findings}" "${fixes}" >> "${GITHUB_STEP_SUMMARY}"
}

# summarize FINDINGS FIXES: the failure as Markdown, for the GitHub Actions step summary.
function summarize() {
  printf '## ❌ Legal file drift\n\n'
  sed 's/^/- /' <<< "${1%$'\n'}"
  printf '\n%s' "${2}"
}

main "${@:-}"
