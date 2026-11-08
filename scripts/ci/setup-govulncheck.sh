#!/usr/bin/env bash
# scripts/ci/setup-govulncheck.sh
# Install a pinned govulncheck into the Go toolchain bin while the tool image is built. No-op if
# that version is present.
#
# Pinned version: v1.9.0
# Project hook: setup-govulncheck.local.sh runs first, when present, with this script's arguments
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

readonly TOOL_MODULE='golang.org/x/vuln/cmd/govulncheck'
readonly TOOL_VERSION='v1.9.0'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  if is_pinned_version_installed; then
    log "✅ govulncheck ${TOOL_VERSION} already installed"
    return 0
  fi
  require_image_build
  log "🔧 Installing govulncheck ${TOOL_VERSION}..."
  go install "${TOOL_MODULE}@${TOOL_VERSION}"
  log "✅ govulncheck ${TOOL_VERSION} installed"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/setup_govulncheck.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

function is_pinned_version_installed() {
  command -v govulncheck >/dev/null 2>&1 && \
    go version -m "$(command -v govulncheck)" 2>/dev/null | grep -qE "^\s+mod\s+\S+\s+${TOOL_VERSION}\b"
}

# require_image_build: tools are installed only while the tool image is built
# (containers/*/Containerfile sets KEELCORE_IMAGE_BUILD); anywhere else the tool is already in the
# image, and a missing or different version is an error, never an install.
function require_image_build() {
  [ -n "${KEELCORE_IMAGE_BUILD:-}" ] && return 0
  log "❌ govulncheck ${TOOL_VERSION} is not in this tool image; tools install only when the image is built"
  exit 1
}

main "${@:-}"
