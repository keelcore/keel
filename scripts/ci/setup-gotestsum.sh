#!/usr/bin/env bash
# scripts/ci/setup-gotestsum.sh
# Install a pinned, checksum-verified gotestsum release binary on Linux (the dev image).
# The release binary: one download, no module fetch or compile.
# No-op if the pinned version is already present.
#
# Pinned version: v1.13.0
# Update GOTESTSUM_VERSION and GOTESTSUM_SHA256_* together when bumping.
# Project hook: setup-gotestsum.local.sh runs first, when present, with this script's arguments
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

readonly GOTESTSUM_VERSION='1.13.0'
readonly GOTESTSUM_SHA256_AMD64='11ccddeaf708ef228889f9fe2f68291a75b27013ddfc3b18156e094f5f40e8ee'
readonly GOTESTSUM_SHA256_ARM64='7644a4c5cd1bb978d56245aeab25a586ac5ac62adebed20a399548867c13499d'
if [ "$(uname -m)" = 'aarch64' ]; then
  readonly GOTESTSUM_SHA256="${GOTESTSUM_SHA256_ARM64}" GOTESTSUM_ARCH='arm64'
else
  readonly GOTESTSUM_SHA256="${GOTESTSUM_SHA256_AMD64}" GOTESTSUM_ARCH='amd64'
fi
readonly GOTESTSUM_TARBALL="gotestsum_${GOTESTSUM_VERSION}_linux_${GOTESTSUM_ARCH}.tar.gz"
readonly GOTESTSUM_URL="https://github.com/gotestyourself/gotestsum/releases/download/v${GOTESTSUM_VERSION}/${GOTESTSUM_TARBALL}"
readonly GOTESTSUM_INSTALL_PATH='/usr/local/bin/gotestsum'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  if is_pinned_version_installed; then
    log "✅ gotestsum v${GOTESTSUM_VERSION} already installed"
    return 0
  fi
  require_linux
  log "🔧 Installing gotestsum v${GOTESTSUM_VERSION}..."
  download_and_verify
  install_gotestsum
  log "✅ gotestsum v${GOTESTSUM_VERSION} installed at ${GOTESTSUM_INSTALL_PATH}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/setup_gotestsum.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

function as_root() {
  if [ "$(id -u)" -eq 0 ]; then "$@"; else sudo "$@"; fi
}

function is_pinned_version_installed() {
  command -v gotestsum >/dev/null 2>&1 && \
    gotestsum --version 2>/dev/null | grep -qF "${GOTESTSUM_VERSION}"
}

function require_linux() {
  if [[ "$(uname -s)" != 'Linux' ]]; then
    log "ERROR: setup-gotestsum.sh installs the Linux release binary (the dev image)"
    exit 1
  fi
}

function download_and_verify() {
  local tmp
  tmp="$(mktemp -d)"
  curl -fsSL --retry 5 --retry-delay 10 --retry-all-errors --connect-timeout 30 --speed-limit 1024 --speed-time 60 "${GOTESTSUM_URL}" -o "${tmp}/${GOTESTSUM_TARBALL}"
  printf '%s  %s/%s\n' "${GOTESTSUM_SHA256}" "${tmp}" "${GOTESTSUM_TARBALL}" | sha256sum --check --quiet
  tar -xz -C "${tmp}" -f "${tmp}/${GOTESTSUM_TARBALL}" gotestsum
  mv "${tmp}/gotestsum" /tmp/gotestsum-staged
  rm -rf "${tmp}"
}

function install_gotestsum() {
  as_root mv /tmp/gotestsum-staged "${GOTESTSUM_INSTALL_PATH}"
  as_root chmod +x "${GOTESTSUM_INSTALL_PATH}"
}

main "${@:-}"
