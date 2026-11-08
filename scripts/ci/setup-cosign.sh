#!/usr/bin/env bash
# setup-cosign.sh
# Install the pinned cosign from its GitHub release, verified against the
# committed SHA-256 for the architecture. Runs when the dev image is built
# (containers/dev/Containerfile); no-op if that version is already installed.
# Project hook: setup-cosign.local.sh runs first, when present, with this script's arguments
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

readonly COSIGN_VERSION='3.1.4'
readonly COSIGN_SHA256_AMD64='424e2c97d0b45fd7a6b608a87f090f35633f4c23ece5f092d48bba11aaf4bbfa'
readonly COSIGN_SHA256_ARM64='ade83b7aac62e131cacfe96d958948b2150cc9efec46a570a2649b8e27dedad2'
readonly COSIGN_RELEASES='https://github.com/sigstore/cosign/releases/download'
readonly COSIGN_INSTALL_PATH='/usr/local/bin/cosign'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  install_cosign
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/setup_cosign.log' >&5
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

function install_cosign() {
  if command -v cosign > /dev/null 2>&1 && cosign version 2>/dev/null | grep -qE "GitVersion:[[:space:]]+v${COSIGN_VERSION}$"; then
    log "✅ cosign ${COSIGN_VERSION} already installed"
    return 0
  fi
  if [[ "$(uname -s)" != 'Linux' ]]; then
    log '❌ cosign installs in the dev image (Linux); run the signing targets through make'
    exit 1
  fi
  require_image_build
  local arch sha binary tmp
  case "$(uname -m)" in
    x86_64) arch='amd64'; sha="${COSIGN_SHA256_AMD64}" ;;
    aarch64) arch='arm64'; sha="${COSIGN_SHA256_ARM64}" ;;
    *) log "❌ Unsupported architecture: $(uname -m)"; exit 1 ;;
  esac
  binary="cosign-linux-${arch}"
  tmp="$(mktemp -d)"
  log "⚓ Installing cosign ${COSIGN_VERSION} (${arch})..."
  curl -fsSL --retry 5 --retry-delay 10 --retry-all-errors --connect-timeout 30 --speed-limit 1024 --speed-time 60 "${COSIGN_RELEASES}/v${COSIGN_VERSION}/${binary}" -o "${tmp}/${binary}"
  printf '%s  %s/%s\n' "${sha}" "${tmp}" "${binary}" | sha256sum --check --quiet
  as_root install -m 0755 "${tmp}/${binary}" "${COSIGN_INSTALL_PATH}"
  rm -rf "${tmp}"
  log "✅ cosign ${COSIGN_VERSION} installed at ${COSIGN_INSTALL_PATH}"
}

# require_image_build: tools are installed only while the tool image is built
# (containers/*/Containerfile sets KEELCORE_IMAGE_BUILD); anywhere else the tool is already in the
# image, and a missing or different version is an error, never an install.
function require_image_build() {
  [ -n "${KEELCORE_IMAGE_BUILD:-}" ] && return 0
  log "❌ cosign ${COSIGN_VERSION} is not in this tool image; tools install only when the image is built"
  exit 1
}

main "${@:-}"
