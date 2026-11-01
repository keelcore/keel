#!/usr/bin/env bash
# scripts/ci/setup-actionlint.sh
# Install a pinned actionlint into the Go toolchain bin. No-op if that version is present.
#
# Pinned version: v1.7.12

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly TOOL_MODULE='github.com/rhysd/actionlint/cmd/actionlint'
readonly TOOL_VERSION='v1.7.12'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  if is_pinned_version_installed; then
    log "✅ actionlint ${TOOL_VERSION} already installed"
    return 0
  fi
  log "🔧 Installing actionlint ${TOOL_VERSION}..."
  go install "${TOOL_MODULE}@${TOOL_VERSION}"
  log "✅ actionlint ${TOOL_VERSION} installed"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_setup_actionlint.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

function is_pinned_version_installed() {
  command -v actionlint >/dev/null 2>&1 && \
    go version -m "$(command -v actionlint)" 2>/dev/null | grep -qE "^\s+mod\s+\S+\s+${TOOL_VERSION}\b"
}

main "${@:-}"
