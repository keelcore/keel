#!/usr/bin/env bash
# setup-grype.sh
# Install a pinned, checksum-verified grype binary on a Linux CI runner.
# Downloads the release tarball and verifies its SHA256 before installing —
# never pipes a remote installer into a shell. No-op if the pinned version is
# already present.
#
# Pinned version: v0.120.1
# Update GRYPE_VERSION and GRYPE_SHA256 together when bumping.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly GRYPE_VERSION='0.120.1'
readonly GRYPE_SHA256_AMD64='0a9ee97ef5ae2ee953b0a80098105052e846cdbe319a57d808b519c33cd1343d'
readonly GRYPE_SHA256_ARM64='29f47391dc283aa79fcc38e65224cd61f64dec0ecfd0db7074128ebf8ff23514'
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
    log "ERROR: setup-grype.sh only supports Linux CI runners"
    log "  For macOS: brew install grype"
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
  curl -fsSL --retry 5 --retry-delay 10 --retry-all-errors "${GRYPE_URL}" -o "${tmp}/${GRYPE_TARBALL}"
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
