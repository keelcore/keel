#!/usr/bin/env bash
# setup-cosign.sh
# Install the pinned cosign from its GitHub release, verified against the
# committed SHA-256 for the architecture. Runs when the dev image is built
# (containers/dev/Containerfile); no-op if that version is already installed.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly COSIGN_VERSION='3.1.3'
readonly COSIGN_SHA256_AMD64='4629c757b7618056f8ddd7e2625ae9fdd94c0372a65049520bc7d9df9efc7f71'
readonly COSIGN_SHA256_ARM64='c5d324e091826b0d7a78eb16fef316450b4eb9aaec045611c08ba06f5e73220a'
readonly COSIGN_RELEASES='https://github.com/sigstore/cosign/releases/download'
readonly COSIGN_INSTALL_PATH='/usr/local/bin/cosign'

function main() {
  exec 5>&1
  validate_args "${@:-}"
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
  local arch sha binary tmp
  case "$(uname -m)" in
    x86_64) arch='amd64'; sha="${COSIGN_SHA256_AMD64}" ;;
    aarch64) arch='arm64'; sha="${COSIGN_SHA256_ARM64}" ;;
    *) log "❌ Unsupported architecture: $(uname -m)"; exit 1 ;;
  esac
  binary="cosign-linux-${arch}"
  tmp="$(mktemp -d)"
  log "⚓ Installing cosign ${COSIGN_VERSION} (${arch})..."
  curl -fsSL "${COSIGN_RELEASES}/v${COSIGN_VERSION}/${binary}" -o "${tmp}/${binary}"
  printf '%s  %s/%s\n' "${sha}" "${tmp}" "${binary}" | sha256sum --check --quiet
  as_root install -m 0755 "${tmp}/${binary}" "${COSIGN_INSTALL_PATH}"
  rm -rf "${tmp}"
  log "✅ cosign ${COSIGN_VERSION} installed at ${COSIGN_INSTALL_PATH}"
}

main "${@:-}"
