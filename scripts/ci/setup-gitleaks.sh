#!/usr/bin/env bash
# setup-gitleaks.sh
# Install the pinned gitleaks from its GitHub release, verified against the
# committed SHA-256 for the architecture. Runs when the dev image is built
# (containers/dev/Containerfile); no-op if that version is already installed.
# Project hook: setup-gitleaks.local.sh runs first, when present, with this script's arguments
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

readonly GITLEAKS_VERSION='8.30.1'
readonly GITLEAKS_SHA256_X64='551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb'
readonly GITLEAKS_SHA256_ARM64='e4a487ee7ccd7d3a7f7ec08657610aa3606637dab924210b3aee62570fb4b080'
readonly GITLEAKS_RELEASES='https://github.com/gitleaks/gitleaks/releases/download'
readonly GITLEAKS_INSTALL_PATH='/usr/local/bin/gitleaks'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  install_gitleaks
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/setup_gitleaks.log' >&5
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

function install_gitleaks() {
  if command -v gitleaks > /dev/null 2>&1 && [ "$(gitleaks version)" = "${GITLEAKS_VERSION}" ]; then
    log "✅ gitleaks ${GITLEAKS_VERSION} already installed"
    return 0
  fi
  if [[ "$(uname -s)" != 'Linux' ]]; then
    log '❌ gitleaks installs in the dev image (Linux); run: make check-secrets'
    exit 1
  fi
  require_image_build
  local arch sha tarball tmp
  case "$(uname -m)" in
    x86_64) arch='x64'; sha="${GITLEAKS_SHA256_X64}" ;;
    aarch64) arch='arm64'; sha="${GITLEAKS_SHA256_ARM64}" ;;
    *) log "❌ Unsupported architecture: $(uname -m)"; exit 1 ;;
  esac
  tarball="gitleaks_${GITLEAKS_VERSION}_linux_${arch}.tar.gz"
  tmp="$(mktemp -d)"
  log "⚓ Installing gitleaks ${GITLEAKS_VERSION} (${arch})..."
  curl -fsSL --retry 5 --retry-delay 10 --retry-all-errors --connect-timeout 30 --speed-limit 1024 --speed-time 60 "${GITLEAKS_RELEASES}/v${GITLEAKS_VERSION}/${tarball}" -o "${tmp}/${tarball}"
  printf '%s  %s/%s\n' "${sha}" "${tmp}" "${tarball}" | sha256sum --check --quiet
  tar -xz -C "${tmp}" -f "${tmp}/${tarball}" gitleaks
  as_root install -m 0755 "${tmp}/gitleaks" "${GITLEAKS_INSTALL_PATH}"
  rm -rf "${tmp}"
  log "✅ gitleaks ${GITLEAKS_VERSION} installed at ${GITLEAKS_INSTALL_PATH}"
}

# require_image_build: tools are installed only while the tool image is built
# (containers/*/Containerfile sets KEELCORE_IMAGE_BUILD); anywhere else the tool is already in the
# image, and a missing or different version is an error, never an install.
function require_image_build() {
  [ -n "${KEELCORE_IMAGE_BUILD:-}" ] && return 0
  log "❌ gitleaks ${GITLEAKS_VERSION} is not in this tool image; tools install only when the image is built"
  exit 1
}

main "${@:-}"
