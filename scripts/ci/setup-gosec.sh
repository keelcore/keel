#!/usr/bin/env bash
# scripts/ci/setup-gosec.sh
# Install a pinned, checksum-verified gosec release binary on Linux (the dev image).
# The release binary, not `go install`: building gosec from source pulls every
# module it imports, including AI SDKs gosec only uses for optional fix suggestions.
# No-op if the pinned version is already present.
#
# Pinned version: v2.29.0
# Update GOSEC_VERSION and GOSEC_SHA256_* together when bumping.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly GOSEC_VERSION='2.30.0'
readonly GOSEC_SHA256_AMD64='3fcce2c4e802ff4169217874602a7df355e9a37bcb6ffaa34b08f3d5f336cdfb'
readonly GOSEC_SHA256_ARM64='0867994920453070a5d6a713e35921a3058473e5c3bf777fdd5a2d6f95313fda'
if [ "$(uname -m)" = 'aarch64' ]; then
  readonly GOSEC_SHA256="${GOSEC_SHA256_ARM64}" GOSEC_ARCH='arm64'
else
  readonly GOSEC_SHA256="${GOSEC_SHA256_AMD64}" GOSEC_ARCH='amd64'
fi
readonly GOSEC_TARBALL="gosec_${GOSEC_VERSION}_linux_${GOSEC_ARCH}.tar.gz"
readonly GOSEC_URL="https://github.com/securego/gosec/releases/download/v${GOSEC_VERSION}/${GOSEC_TARBALL}"
readonly GOSEC_INSTALL_PATH='/usr/local/bin/gosec'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  if is_pinned_version_installed; then
    log "✅ gosec v${GOSEC_VERSION} already installed"
    return 0
  fi
  require_linux
  log "🔧 Installing gosec v${GOSEC_VERSION}..."
  download_and_verify
  install_gosec
  log "✅ gosec v${GOSEC_VERSION} installed at ${GOSEC_INSTALL_PATH}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/setup_gosec.log' >&5
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
  command -v gosec >/dev/null 2>&1 && \
    gosec -version 2>/dev/null | grep -qF "${GOSEC_VERSION}"
}

function require_linux() {
  if [[ "$(uname -s)" != 'Linux' ]]; then
    log "ERROR: setup-gosec.sh installs the Linux release binary (the dev image)"
    exit 1
  fi
}

function download_and_verify() {
  local tmp
  tmp="$(mktemp -d)"
  curl -fsSL --retry 5 --retry-delay 10 --retry-all-errors --connect-timeout 30 --speed-limit 1024 --speed-time 60 "${GOSEC_URL}" -o "${tmp}/${GOSEC_TARBALL}"
  printf '%s  %s/%s\n' "${GOSEC_SHA256}" "${tmp}" "${GOSEC_TARBALL}" | sha256sum --check --quiet
  tar -xz -C "${tmp}" -f "${tmp}/${GOSEC_TARBALL}" gosec
  mv "${tmp}/gosec" /tmp/gosec-staged
  rm -rf "${tmp}"
}

function install_gosec() {
  as_root mv /tmp/gosec-staged "${GOSEC_INSTALL_PATH}"
  as_root chmod +x "${GOSEC_INSTALL_PATH}"
}

main "${@:-}"
