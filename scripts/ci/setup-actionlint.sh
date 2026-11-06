#!/usr/bin/env bash
# scripts/ci/setup-actionlint.sh
# Install a pinned, checksum-verified actionlint release binary on Linux (the dev image).
# The release binary: one download, no module fetch or compile.
# No-op if the pinned version is already present.
#
# Pinned version: v1.7.12
# Update ACTIONLINT_VERSION and ACTIONLINT_SHA256_* together when bumping.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly ACTIONLINT_VERSION='1.7.12'
readonly ACTIONLINT_SHA256_AMD64='8aca8db96f1b94770f1b0d72b6dddcb1ebb8123cb3712530b08cc387b349a3d8'
readonly ACTIONLINT_SHA256_ARM64='325e971b6ba9bfa504672e29be93c24981eeb1c07576d730e9f7c8805afff0c6'
if [ "$(uname -m)" = 'aarch64' ]; then
  readonly ACTIONLINT_SHA256="${ACTIONLINT_SHA256_ARM64}" ACTIONLINT_ARCH='arm64'
else
  readonly ACTIONLINT_SHA256="${ACTIONLINT_SHA256_AMD64}" ACTIONLINT_ARCH='amd64'
fi
readonly ACTIONLINT_TARBALL="actionlint_${ACTIONLINT_VERSION}_linux_${ACTIONLINT_ARCH}.tar.gz"
readonly ACTIONLINT_URL="https://github.com/rhysd/actionlint/releases/download/v${ACTIONLINT_VERSION}/${ACTIONLINT_TARBALL}"
readonly ACTIONLINT_INSTALL_PATH='/usr/local/bin/actionlint'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  if is_pinned_version_installed; then
    log "✅ actionlint v${ACTIONLINT_VERSION} already installed"
    return 0
  fi
  require_linux
  log "🔧 Installing actionlint v${ACTIONLINT_VERSION}..."
  download_and_verify
  install_actionlint
  log "✅ actionlint v${ACTIONLINT_VERSION} installed at ${ACTIONLINT_INSTALL_PATH}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/setup_actionlint.log' >&5
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
  command -v actionlint >/dev/null 2>&1 && \
    actionlint -version 2>/dev/null | grep -qF "${ACTIONLINT_VERSION}"
}

function require_linux() {
  if [[ "$(uname -s)" != 'Linux' ]]; then
    log "ERROR: setup-actionlint.sh installs the Linux release binary (the dev image)"
    exit 1
  fi
}

function download_and_verify() {
  local tmp
  tmp="$(mktemp -d)"
  curl -fsSL --retry 5 --retry-delay 10 --retry-all-errors "${ACTIONLINT_URL}" -o "${tmp}/${ACTIONLINT_TARBALL}"
  printf '%s  %s/%s\n' "${ACTIONLINT_SHA256}" "${tmp}" "${ACTIONLINT_TARBALL}" | sha256sum --check --quiet
  tar -xz -C "${tmp}" -f "${tmp}/${ACTIONLINT_TARBALL}" actionlint
  mv "${tmp}/actionlint" /tmp/actionlint-staged
  rm -rf "${tmp}"
}

function install_actionlint() {
  as_root mv /tmp/actionlint-staged "${ACTIONLINT_INSTALL_PATH}"
  as_root chmod +x "${ACTIONLINT_INSTALL_PATH}"
}

main "${@:-}"
