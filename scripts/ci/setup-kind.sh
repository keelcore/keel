#!/usr/bin/env bash
# setup-kind.sh
# Install the pinned kind in the dev image (Linux, amd64 or arm64), verified against
# the release's published SHA-256. Idempotent: no-op if that version is installed.
# Clusters are created by scripts/k8s/setup-k8s-kind.sh (make setup-k8s-kind).
#
# Update KIND_VERSION and both KIND_SHA256_* together when bumping.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly KIND_VERSION='0.31.0'
# Published as kind-linux-<arch>.sha256sum in the release.
readonly KIND_SHA256_AMD64='eb244cbafcc157dff60cf68693c14c9a75c4e6e6fedaf9cd71c58117cb93e3fa'
readonly KIND_SHA256_ARM64='8e1014e87c34901cc422a1445866835d1e666f2a61301c27e722bdeab5a1f7e4'
readonly KIND_INSTALL_PATH='/usr/local/bin/kind'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  if is_pinned_version_installed; then
    log "kind v${KIND_VERSION} already installed"
    return 0
  fi
  case "$(uname -s)" in
    Linux)
      log "Installing kind v${KIND_VERSION} (Linux)"
      install_linux
      ;;
    *)
      log '❌ kind installs in the dev image (Linux); run its targets through make'
      exit 1
      ;;
  esac
  log "kind installed: $(kind version)"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_setup_kind.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected arg'
    exit 1
  fi
}

function as_root() {
  if [ "$(id -u)" -eq 0 ]; then "$@"; else sudo "$@"; fi
}

function is_pinned_version_installed() {
  command -v kind >/dev/null 2>&1 && \
    kind version 2>/dev/null | grep -qF "v${KIND_VERSION}"
}

function install_linux() {
  local arch sum
  arch="$(uname -m)"
  case "${arch}" in
    x86_64)  arch='amd64'; sum="${KIND_SHA256_AMD64}" ;;
    aarch64) arch='arm64'; sum="${KIND_SHA256_ARM64}" ;;
    *)
      log "ERROR: unsupported arch: ${arch}"
      exit 1
      ;;
  esac
  local -r url="https://github.com/kubernetes-sigs/kind/releases/download/v${KIND_VERSION}/kind-linux-${arch}"
  local tmp
  tmp="$(mktemp -d)"
  curl -fsSL --retry 5 --retry-delay 10 --retry-all-errors --connect-timeout 30 --speed-limit 1024 --speed-time 60 "${url}" -o "${tmp}/kind"
  printf '%s  %s\n' "${sum}" "${tmp}/kind" | sha256sum --check --quiet
  as_root install -m 0755 "${tmp}/kind" "${KIND_INSTALL_PATH}"
  rm -rf "${tmp}"
}

main "${@:-}"
