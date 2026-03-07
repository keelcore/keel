#!/usr/bin/env bash
# install-cosign.sh
# Install a pinned cosign binary on a Linux CI runner.
# Downloads the binary and verifies its SHA256 checksum before installing.
# No-op if cosign is already present at the pinned version.
#
# Pinned version: v2.4.3
# Update COSIGN_VERSION and COSIGN_SHA256 together when bumping.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly COSIGN_VERSION='2.4.3'
readonly COSIGN_BINARY_URL="https://github.com/sigstore/cosign/releases/download/v${COSIGN_VERSION}/cosign-linux-amd64"
readonly COSIGN_SHA256_URL="${COSIGN_BINARY_URL}.sha256"
readonly COSIGN_INSTALL_PATH='/usr/local/bin/cosign'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  if is_pinned_version_installed; then
    log "cosign ${COSIGN_VERSION} already installed"
    return 0
  fi
  require_linux
  log "Installing cosign v${COSIGN_VERSION}"
  download_and_verify
  install_cosign
  log "cosign v${COSIGN_VERSION} installed at ${COSIGN_INSTALL_PATH}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_install_cosign.log' >&5
}

function validate_args() { :; }

function is_pinned_version_installed() {
  command -v cosign >/dev/null 2>&1 && \
    cosign version 2>/dev/null | grep -qF "v${COSIGN_VERSION}"
}

function require_linux() {
  if [[ "$(uname -s)" != 'Linux' ]]; then
    log "ERROR: install-cosign.sh only supports Linux CI runners"
    log "  For macOS: brew install cosign"
    exit 1
  fi
}

function download_and_verify() {
  local tmp
  tmp="$(mktemp -d)"
  download_binary "${tmp}"
  verify_checksum "${tmp}"
  mv "${tmp}/cosign" /tmp/cosign-staged
  rm -rf "${tmp}"
}

function download_binary() {
  local -r tmp="${1}"
  curl -fsSL "${COSIGN_BINARY_URL}" -o "${tmp}/cosign"
  curl -fsSL "${COSIGN_SHA256_URL}" -o "${tmp}/cosign.sha256"
}

function verify_checksum() {
  local -r tmp="${1}"
  local -r expected
  expected="$(awk '{print $1}' "${tmp}/cosign.sha256")"
  printf '%s  %s/cosign\n' "${expected}" "${tmp}" | sha256sum --check --quiet
}

function install_cosign() {
  sudo mv /tmp/cosign-staged "${COSIGN_INSTALL_PATH}"
  sudo chmod +x "${COSIGN_INSTALL_PATH}"
}

main "${@:-}"