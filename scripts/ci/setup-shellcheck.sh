#!/usr/bin/env bash
# setup-shellcheck.sh
# Install the pinned shellcheck from its GitHub release (Linux; runs when the dev
# image is built), verified against the committed SHA-256 for the architecture.
# No-op if that version is already installed.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly SHELLCHECK_VERSION='0.11.0'
readonly SHELLCHECK_SHA256_X86_64='8c3be12b05d5c177a04c29e3c78ce89ac86f1595681cab149b65b97c4e227198'
readonly SHELLCHECK_SHA256_AARCH64='12b331c1d2db6b9eb13cfca64306b1b157a86eb69db83023e261eaa7e7c14588'
readonly SHELLCHECK_RELEASES='https://github.com/koalaman/shellcheck/releases/download'
readonly SHELLCHECK_INSTALL_PATH='/usr/local/bin/shellcheck'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  install_shellcheck
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/setup_shellcheck.log' >&5
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
  command -v shellcheck > /dev/null 2>&1 && \
    shellcheck --version | grep -qE "^version: ${SHELLCHECK_VERSION}$"
}

function install_shellcheck() {
  if is_pinned_version_installed; then
    log "✅ shellcheck ${SHELLCHECK_VERSION} already installed"
    return 0
  fi
  if [[ "$(uname -s)" != 'Linux' ]]; then
    log '❌ shellcheck installs in the dev image (Linux); run: make lint-bash'
    exit 1
  fi
  require_image_build
  local arch sha tarball tmp
  arch="$(uname -m)"
  case "${arch}" in
    x86_64) sha="${SHELLCHECK_SHA256_X86_64}" ;;
    aarch64) sha="${SHELLCHECK_SHA256_AARCH64}" ;;
    *) log "❌ Unsupported architecture: ${arch}"; exit 1 ;;
  esac
  tarball="shellcheck-v${SHELLCHECK_VERSION}.linux.${arch}.tar.xz"
  tmp="$(mktemp -d)"
  log "⚓ Installing shellcheck ${SHELLCHECK_VERSION} (${arch})..."
  curl -fsSL --retry 5 --retry-delay 10 --retry-all-errors --connect-timeout 30 --speed-limit 1024 --speed-time 60 "${SHELLCHECK_RELEASES}/v${SHELLCHECK_VERSION}/${tarball}" -o "${tmp}/${tarball}"
  printf '%s  %s/%s\n' "${sha}" "${tmp}" "${tarball}" | sha256sum --check --quiet
  tar -xJ -C "${tmp}" -f "${tmp}/${tarball}"
  as_root install -m 0755 "${tmp}/shellcheck-v${SHELLCHECK_VERSION}/shellcheck" "${SHELLCHECK_INSTALL_PATH}"
  rm -rf "${tmp}"
  log "✅ shellcheck ${SHELLCHECK_VERSION} installed at ${SHELLCHECK_INSTALL_PATH}"
}

# require_image_build: tools are installed only while the tool image is built
# (containers/*/Containerfile sets KEELCORE_IMAGE_BUILD); anywhere else the tool is already in the
# image, and a missing or different version is an error, never an install.
function require_image_build() {
  [ -n "${KEELCORE_IMAGE_BUILD:-}" ] && return 0
  log "❌ shellcheck ${SHELLCHECK_VERSION} is not in this tool image; tools install only when the image is built"
  exit 1
}

main "${@:-}"
