#!/usr/bin/env bash
# scripts/build/dist-chmod.sh
# Make the built dist/keel-* release binaries executable, and, with DIST_OWNER (uid:gid, set by
# make dist-chmod on Linux), hand dist/ back to the host user: on a Linux runner the dev image
# runs as root, so files it writes there (cosign bundles, 0600) are unreadable to host steps.

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
  log '🔑 Making dist binaries executable...'
  chmod +x dist/keel-*
  if [ -n "${DIST_OWNER:-}" ]; then
    log "🔑 Handing dist/ to ${DIST_OWNER}..."
    chown -R "${DIST_OWNER}" dist
  fi
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_dist_chmod.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
