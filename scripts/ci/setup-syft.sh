#!/usr/bin/env bash
# setup-syft.sh
# Install a pinned, checksum-verified syft binary (Linux; runs when the dev image is built).
# Downloads the release tarball and verifies its SHA256 before installing —
# never pipes a remote installer into a shell. No-op if the pinned version is
# already present.
#
# Pinned version: v1.42.1
# Update SYFT_VERSION and the SYFT_SHA256_* sums together when bumping (make update-pins does).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly SYFT_VERSION='1.55.0'
readonly SYFT_SHA256_AMD64='bb65249542db5fedeeb4d3f9b018b75e7264dbfbbb10486f044581a92f90490d'
readonly SYFT_SHA256_ARM64='929bd163db2d7cd2255d3bcb8967208079813d7b46b0c952f7b794d8e2c7d304'
if [ "$(uname -m)" = 'aarch64' ]; then
  readonly SYFT_ARCH='arm64' SYFT_SHA256="${SYFT_SHA256_ARM64}"
else
  readonly SYFT_ARCH='amd64' SYFT_SHA256="${SYFT_SHA256_AMD64}"
fi
readonly SYFT_TARBALL="syft_${SYFT_VERSION}_linux_${SYFT_ARCH}.tar.gz"
readonly SYFT_URL="https://github.com/anchore/syft/releases/download/v${SYFT_VERSION}/${SYFT_TARBALL}"
readonly SYFT_INSTALL_PATH='/usr/local/bin/syft'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  if is_pinned_version_installed; then
    log "✅ syft ${SYFT_VERSION} already installed"
    return 0
  fi
  require_linux
  require_image_build
  log "⚓ Installing syft v${SYFT_VERSION}"
  download_and_verify
  install_syft
  log "✅ syft v${SYFT_VERSION} installed at ${SYFT_INSTALL_PATH}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/setup_syft.log' >&5
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
  command -v syft >/dev/null 2>&1 && \
    syft version 2>/dev/null | grep -qF "${SYFT_VERSION}"
}

function require_linux() {
  if [[ "$(uname -s)" != 'Linux' ]]; then
    log 'ERROR: syft installs in the dev image (Linux); run the release targets through make'
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
  curl -fsSL --retry 5 --retry-delay 10 --retry-all-errors --connect-timeout 30 --speed-limit 1024 --speed-time 60 "${SYFT_URL}" -o "${tmp}/${SYFT_TARBALL}"
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
  as_root mv /tmp/syft-staged "${SYFT_INSTALL_PATH}"
  as_root chmod +x "${SYFT_INSTALL_PATH}"
}

# require_image_build: tools are installed only while the tool image is built
# (containers/*/Containerfile sets KEELCORE_IMAGE_BUILD); anywhere else the tool is already in the
# image, and a missing or different version is an error, never an install.
function require_image_build() {
  [ -n "${KEELCORE_IMAGE_BUILD:-}" ] && return 0
  log "❌ syft v${SYFT_VERSION} is not in this tool image; tools install only when the image is built"
  exit 1
}

main "${@:-}"
