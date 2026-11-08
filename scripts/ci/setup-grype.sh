#!/usr/bin/env bash
# setup-grype.sh
# Install a pinned, checksum-verified grype binary on a Linux CI runner.
# Downloads the release tarball and verifies its SHA256 before installing —
# never pipes a remote installer into a shell. No-op if the pinned version is
# already present.
#
# Pinned version: v0.120.1
# Update GRYPE_VERSION and GRYPE_SHA256 together when bumping.
# Project hook: setup-grype.local.sh runs first, when present, with this script's arguments
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

readonly GRYPE_VERSION='0.121.0'
readonly GRYPE_SHA256_AMD64='64e07133d3486115de647a66581461dccf5c732748a0a3119077395565f15519'
readonly GRYPE_SHA256_ARM64='86f570f75ca947ea3dd6486309232bcff6d78e44ba081ae75ab14eab72be3dae'
if [ "$(uname -m)" = 'aarch64' ]; then
  readonly GRYPE_SHA256="${GRYPE_SHA256_ARM64}" GRYPE_ARCH='arm64'
else
  readonly GRYPE_SHA256="${GRYPE_SHA256_AMD64}" GRYPE_ARCH='amd64'
fi
readonly GRYPE_TARBALL="grype_${GRYPE_VERSION}_linux_${GRYPE_ARCH}.tar.gz"
readonly GRYPE_URL="https://github.com/anchore/grype/releases/download/v${GRYPE_VERSION}/${GRYPE_TARBALL}"
readonly GRYPE_INSTALL_PATH='/usr/local/bin/grype'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  if is_pinned_version_installed; then
    log "✅ grype ${GRYPE_VERSION} already installed"
    return 0
  fi
  require_linux
  log "⚓ Installing grype v${GRYPE_VERSION}"
  download_and_verify
  install_grype
  log "✅ grype v${GRYPE_VERSION} installed at ${GRYPE_INSTALL_PATH}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/setup_grype.log' >&5
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
  command -v grype >/dev/null 2>&1 && \
    grype version 2>/dev/null | grep -qF "${GRYPE_VERSION}"
}

function require_linux() {
  if [[ "$(uname -s)" != 'Linux' ]]; then
    log '❌ grype installs in the dev image (Linux); run its targets through make'
    exit 1
  fi
}

function download_and_verify() {
  local tmp
  tmp="$(mktemp -d)"
  download_tarball "${tmp}"
  verify_checksum "${tmp}"
  extract_binary "${tmp}"
  rm -rf "${tmp}"
}

function download_tarball() {
  local -r tmp="${1}"
  curl -fsSL --retry 5 --retry-delay 10 --retry-all-errors --connect-timeout 30 --speed-limit 1024 --speed-time 60 "${GRYPE_URL}" -o "${tmp}/${GRYPE_TARBALL}"
}

function verify_checksum() {
  local -r tmp="${1}"
  printf '%s  %s/%s\n' "${GRYPE_SHA256}" "${tmp}" "${GRYPE_TARBALL}" \
    | sha256sum --check --quiet
}

function extract_binary() {
  local -r tmp="${1}"
  tar -xz -C "${tmp}" -f "${tmp}/${GRYPE_TARBALL}" grype
  mv "${tmp}/grype" /tmp/grype-staged
}

function install_grype() {
  as_root mv /tmp/grype-staged "${GRYPE_INSTALL_PATH}"
  as_root chmod +x "${GRYPE_INSTALL_PATH}"
}

main "${@:-}"
