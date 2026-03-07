#!/usr/bin/env bash
# install-syft.sh
# Install a pinned syft binary on a Linux CI runner.
# Downloads the tarball and verifies its SHA256 checksum before installing.
# No-op if syft is already present at the pinned version.
#
# Pinned version: v1.42.1
# Update SYFT_VERSION and SYFT_SHA256 together when bumping.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly SYFT_VERSION='1.42.1'
readonly SYFT_SHA256='989ded4e772810f93de6ccdc4512f79a6dabb5fb2dd2a9ffc72a80c955e6125a'
readonly SYFT_TARBALL="syft_${SYFT_VERSION}_linux_amd64.tar.gz"
readonly SYFT_URL="https://github.com/anchore/syft/releases/download/v${SYFT_VERSION}/${SYFT_TARBALL}"
readonly SYFT_INSTALL_PATH='/usr/local/bin/syft'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  if is_pinned_version_installed; then
    log "syft ${SYFT_VERSION} already installed"
    return 0
  fi
  require_linux
  log "Installing syft v${SYFT_VERSION}"
  download_and_verify
  install_syft
  log "syft v${SYFT_VERSION} installed at ${SYFT_INSTALL_PATH}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_install_syft.log' >&5
}

function validate_args() { :; }

function is_pinned_version_installed() {
  command -v syft >/dev/null 2>&1 && \
    syft version 2>/dev/null | grep -qF "${SYFT_VERSION}"
}

function require_linux() {
  if [[ "$(uname -s)" != 'Linux' ]]; then
    log "ERROR: install-syft.sh only supports Linux CI runners"
    log "  For macOS: brew install syft"
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
  curl -fsSL "${SYFT_URL}" -o "${tmp}/${SYFT_TARBALL}"
}

function verify_checksum() {
  local -r tmp="${1}"
  printf '%s  %s/%s\n' "${SYFT_SHA256}" "${tmp}" "${SYFT_TARBALL}" \
    | sha256sum --check --quiet
}

function extract_binary() {
  local -r tmp="${1}"
  tar -xz -C "${tmp}" -f "${tmp}/${SYFT_TARBALL}" syft
  mv "${tmp}/syft" /tmp/syft-staged
}

function install_syft() {
  sudo mv /tmp/syft-staged "${SYFT_INSTALL_PATH}"
  sudo chmod +x "${SYFT_INSTALL_PATH}"
}

main "${@:-}"
