#!/usr/bin/env bash
# setup-apple-container.sh
# Install Apple container from the signed installer package of its latest GitHub release, Apple's
# documented method, verified against the release's published SHA-256 and Apple's signature. Runs on
# the host (macOS on Apple silicon); no-op if that version is already installed. Then starts the system
# and installs its default kernel when it has none.
# Project hook: setup-apple-container.local.sh runs first, when present, with this script's arguments
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

# shellcheck source=../lib/container-system.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/container-system.sh"

readonly CONTAINER_LATEST='https://api.github.com/repos/apple/container/releases/latest'
readonly CONTAINER_SIGNER='Developer ID Installer: Apple Inc. - Containerization (UPBK2H6LZM)'
readonly CURL_RETRY=(--retry 5 --retry-delay 10 --retry-all-errors --connect-timeout 30 --speed-limit 1024 --speed-time 60)

WORK_DIR=''

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  require_platform
  install_container
  start_container_system
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/setup_apple_container.log' >&5
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

function require_platform() {
  [ "$(uname -s)" = 'Darwin' ] && [ "$(uname -m)" = 'arm64' ] && return 0
  log '❌ Apple container runs on macOS on Apple silicon'
  exit 1
}

function installed_version() {
  command -v container > /dev/null 2>&1 || return 0
  container --version | sed -nE 's/.*version ([^ ]+).*/\1/p'
}

function require_package_install() {
  case "$(command -v container || true)" in
    */homebrew/* | */Cellar/*)
      log '❌ container is installed by Homebrew; run: brew uninstall container'
      exit 1
      ;;
  esac
}

function verify_package() {
  local -r pkg="${1}" sha="${2}"
  printf '%s  %s\n' "${sha}" "${pkg}" | shasum -a 256 --check --quiet
  local signature
  signature="$(pkgutil --check-signature "${pkg}")"
  if ! grep -qF "${CONTAINER_SIGNER}" <<< "${signature}" \
    || ! grep -qF 'trusted by the Apple notary service' <<< "${signature}"; then
    log "❌ ${pkg}: signature is not ${CONTAINER_SIGNER}, notarized"
    exit 1
  fi
}

function remove_work_dir() {
  [ -z "${WORK_DIR}" ] || rm -rf "${WORK_DIR}"
}

function install_container() {
  require_package_install
  local release version asset url digest current
  release="$(curl -fsSL "${CURL_RETRY[@]}" "${CONTAINER_LATEST}")"
  version="$(jq -r '.tag_name' <<< "${release}")"
  asset="container-${version}-installer-signed.pkg"
  url="$(jq -r --arg a "${asset}" '.assets[] | select(.name == $a) | .browser_download_url' <<< "${release}")"
  digest="$(jq -r --arg a "${asset}" '.assets[] | select(.name == $a) | .digest' <<< "${release}")"
  if [ -z "${url}" ] || ! grep -qE '^sha256:[0-9a-f]{64}$' <<< "${digest}"; then
    log "❌ release ${version} publishes no ${asset} with a SHA-256 digest"
    exit 1
  fi
  current="$(installed_version)"
  if [ "${current}" = "${version}" ]; then
    log "✅ container ${version} already installed"
    return 0
  fi
  trap remove_work_dir EXIT
  WORK_DIR="$(mktemp -d)"
  log "⚓ Installing container ${version}..."
  curl -fsSL "${CURL_RETRY[@]}" "${url}" -o "${WORK_DIR}/${asset}"
  verify_package "${WORK_DIR}/${asset}" "${digest#sha256:}"
  [ -z "${current}" ] || container system stop
  as_root installer -pkg "${WORK_DIR}/${asset}" -target /
  current="$(installed_version)"
  if [ "${current}" != "${version}" ]; then
    log "❌ container reports version ${current:-none} after installing ${version}"
    exit 1
  fi
  log "✅ container ${version} installed"
}

main "${@:-}"
