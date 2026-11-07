#!/usr/bin/env bash
# scripts/ci/setup-staticcheck.sh
# Install a pinned, checksum-verified staticcheck release binary on Linux (the dev image).
# The release binary, not `go install`: one download, no module fetch or compile.
# No-op if the pinned version is already present.
#
# Pinned version: 2026.2.2 (module v0.8.2)
# Update STATICCHECK_VERSION and STATICCHECK_SHA256_* together when bumping.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly STATICCHECK_VERSION='2026.2.2'
readonly STATICCHECK_SHA256_AMD64='14c081e0f0d05b5a4d4c53fd3d357d4a5af9d20501465fefc9024ee828d3aa5c'
readonly STATICCHECK_SHA256_ARM64='ccbdaaf81c37eb246a6063f811ee3b89e02ff042cb068cd1ca73a7377e2f7045'
readonly STATICCHECK_RELEASES='https://github.com/dominikh/go-tools/releases/download'
readonly STATICCHECK_INSTALL_PATH='/usr/local/bin/staticcheck'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  install_staticcheck
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/setup_staticcheck.log' >&5
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

function install_staticcheck() {
  if command -v staticcheck > /dev/null 2>&1 \
    && staticcheck -version 2> /dev/null | grep -qF "staticcheck ${STATICCHECK_VERSION} "; then
    log "✅ staticcheck ${STATICCHECK_VERSION} already installed"
    return 0
  fi
  if [[ "$(uname -s)" != 'Linux' ]]; then
    log '❌ staticcheck installs in the dev image (Linux); run: make lint-go'
    exit 1
  fi
  require_image_build
  local arch sha tarball tmp
  case "$(uname -m)" in
    x86_64) arch='amd64'; sha="${STATICCHECK_SHA256_AMD64}" ;;
    aarch64) arch='arm64'; sha="${STATICCHECK_SHA256_ARM64}" ;;
    *) log "❌ Unsupported architecture: $(uname -m)"; exit 1 ;;
  esac
  tarball="staticcheck_linux_${arch}.tar.gz"
  tmp="$(mktemp -d)"
  log "⚓ Installing staticcheck ${STATICCHECK_VERSION} (${arch})..."
  curl -fsSL --retry 5 --retry-delay 10 --retry-all-errors --connect-timeout 30 --speed-limit 1024 --speed-time 60 "${STATICCHECK_RELEASES}/${STATICCHECK_VERSION}/${tarball}" -o "${tmp}/${tarball}"
  printf '%s  %s/%s\n' "${sha}" "${tmp}" "${tarball}" | sha256sum --check --quiet
  tar -xz -C "${tmp}" -f "${tmp}/${tarball}" staticcheck/staticcheck
  as_root install -m 0755 "${tmp}/staticcheck/staticcheck" "${STATICCHECK_INSTALL_PATH}"
  rm -rf "${tmp}"
  log "✅ staticcheck ${STATICCHECK_VERSION} installed at ${STATICCHECK_INSTALL_PATH}"
}

# require_image_build: tools are installed only while the tool image is built
# (containers/*/Containerfile sets KEELCORE_IMAGE_BUILD); anywhere else the tool is already in the
# image, and a missing or different version is an error, never an install.
function require_image_build() {
  [ -n "${KEELCORE_IMAGE_BUILD:-}" ] && return 0
  log "❌ staticcheck ${STATICCHECK_VERSION} is not in this tool image; tools install only when the image is built"
  exit 1
}

main "${@:-}"
