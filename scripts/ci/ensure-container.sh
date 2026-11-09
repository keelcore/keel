#!/usr/bin/env bash
# ensure-container.sh
# The container runtime every tool-image make target needs (make ensure-container, an order-only prerequisite
# of each): on a Mac, Apple container is installed when missing (setup-apple-container.sh) and its system
# started when stopped. A Linux host (docker) and a run inside a tool image need nothing. The commit hook does
# not call this: it only checks, offline (require_container_system in scripts/lib/container-system.sh).
# Project hook: ensure-container.local.sh runs first, when present, with this script's arguments
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

# shellcheck source=../lib/container-system.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/container-system.sh"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  ensure_container
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/ensure_container.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

function ensure_container() {
  uses_apple_container || return 0
  if ! command -v container > /dev/null 2>&1; then
    log '⚓ Apple container is not installed; installing it'
    bash "$(dirname "${BASH_SOURCE[0]}")/setup-apple-container.sh"
    return 0
  fi
  start_container_system
}

main "${@:-}"
