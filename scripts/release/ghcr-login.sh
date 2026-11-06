#!/usr/bin/env bash
# scripts/release/ghcr-login.sh
# Log the runner's docker in to GHCR, for a job whose action pushes to the registry itself
# (actions/attest-build-provenance with push-to-registry).
#
# Required environment variables:
#   GITHUB_TOKEN   — GHCR write token (GITHUB_TOKEN in Actions)
#   GITHUB_ACTOR   — registry username (github.actor in Actions)

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly REGISTRY='ghcr.io'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  log "Logging in to ${REGISTRY}"
  printf '%s' "${GITHUB_TOKEN:?GITHUB_TOKEN is required}" | \
    docker login "${REGISTRY}" --username "${GITHUB_ACTOR:?GITHUB_ACTOR is required}" --password-stdin
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/ghcr_login.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
