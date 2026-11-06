#!/usr/bin/env bash
# scripts/build/dist-chmod.sh
# Make the release binaries in dist/ executable (artifacts lose their mode in transit), and hand dist/
# to DIST_OWNER when it is set.

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
  log '🔑 Making dist binaries executable...'
  local file
  while IFS= read -r file; do
    chmod +x "${file}"
  done < <(release_files)
  if [ -n "${DIST_OWNER:-}" ]; then
    log "🔑 Handing dist/ to ${DIST_OWNER}..."
    chown -R "${DIST_OWNER}" dist
  fi
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/dist_chmod.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
