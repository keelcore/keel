#!/usr/bin/env bash
# ensure-legal-notices.sh
# Makes every legal-file copy the repository lists in config/legal-copies.local.txt
# (scripts/lib/legal-copies.sh) identical to its source: writes a copy that is missing or differs, with its
# directory, and leaves a matching one untouched. Never writes a source: a missing source fails before any
# copy is written. No list, nothing to write. `make check-legal-drift` verifies the result.
# Project hook: ensure-legal-notices.local.sh runs first, when present, with this script's arguments
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

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  cd "$(git rev-parse --show-toplevel)"
  local pairs
  pairs="$(legal_copies)"
  if [ -z "${pairs}" ]; then
    log "✅ No legal copies listed in ${LEGAL_COPIES_FILE}: nothing to write"
    return 0
  fi
  check_writable "${pairs}"
  local src dst written=0
  while IFS=$'\t' read -r src dst; do
    cmp -s "${src}" "${dst}" && continue
    mkdir -p "$(dirname "${dst}")"
    cp "${src}" "${dst}"
    log "📝 ${dst} ← ${src}"
    written=$((written + 1))
  done <<< "${pairs}"
  log "✅ Every legal copy matches its source (${written} written)"
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/ensure-legal-notices.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 0 ] && [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected arg'
    exit 1
  fi
}

# check_writable PAIRS: every source exists and no copy is a directory; otherwise fails, naming each, before
# any copy is written.
function check_writable() {
  local src dst bad=0
  while IFS=$'\t' read -r src dst; do
    if [ ! -f "${src}" ]; then
      log "❌ ${src} → ${dst}: source missing; restore it, or correct ${LEGAL_COPIES_FILE}"
      bad=1
    elif [ -d "${dst}" ]; then
      log "❌ ${src} → ${dst}: copy is a directory"
      bad=1
    fi
  done <<< "${1}"
  [ "${bad}" -eq 0 ] || exit 1
}

main "${@:-}"
