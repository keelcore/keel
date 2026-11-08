#!/usr/bin/env bash
# scripts/ci/setup-go-licenses.sh
# Install a pinned go-licenses into the Go toolchain bin. No-op if that version is present.
#
# Pinned version: v2.0.1
# Project hook: setup-go-licenses.local.sh runs first, when present, with this script's arguments
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

readonly TOOL_MODULE='github.com/google/go-licenses/v2'
readonly TOOL_VERSION='v2.0.1'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  if is_pinned_version_installed; then
    log "✅ go-licenses ${TOOL_VERSION} already installed"
    return 0
  fi
  log "🔧 Installing go-licenses ${TOOL_VERSION}..."
  go install "${TOOL_MODULE}@${TOOL_VERSION}"
  log "✅ go-licenses ${TOOL_VERSION} installed"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/setup_go-licenses.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

function is_pinned_version_installed() {
  command -v go-licenses >/dev/null 2>&1 && \
    go version -m "$(command -v go-licenses)" 2>/dev/null | grep -qE "^\s+mod\s+\S+\s+${TOOL_VERSION}\b"
}

main "${@:-}"
