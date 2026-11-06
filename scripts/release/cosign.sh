#!/usr/bin/env bash
# cosign.sh
# Run cosign in the dev image against GHCR: log cosign in to the registry, then
# run `cosign ARGS...`. Each scripts/dev/run.sh container starts without registry
# credentials, so every cosign call that reads or writes GHCR goes through here.
#
# Usage: cosign.sh COSIGN_ARG...   (inside the dev image: bash scripts/dev/run.sh bash scripts/release/cosign.sh ...)
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
  printf '%s' "${GITHUB_TOKEN:?GITHUB_TOKEN is required}" | \
    cosign login "${REGISTRY}" --username "${GITHUB_ACTOR:?GITHUB_ACTOR is required}" --password-stdin >&2
  cosign "$@"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/cosign.log' >&5
}

function validate_args() {
  if [ "${#}" -eq 0 ] || [ -z "${1:-}" ]; then
    log '❌ Usage: cosign.sh COSIGN_ARG...'
    exit 1
  fi
}

main "${@:-}"
