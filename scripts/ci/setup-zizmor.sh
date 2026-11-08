#!/usr/bin/env bash
# setup-zizmor.sh
# Install zizmor for GitHub Actions workflow security lint (scripts/lint/lint-workflows.sh).
# Linux (built into the dev image): pinned v1.30.1 release asset, SHA256-verified before install.
# No-op if zizmor is already present.
#
# Pinned version: v1.30.1
# Update ZIZMOR_VERSION and ZIZMOR_SHA256_* together when bumping (make update-pins does).
# Project hook: setup-zizmor.local.sh runs first, when present, with this script's arguments
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

readonly ZIZMOR_VERSION='1.31.1'
readonly ZIZMOR_SHA256_X86_64='d185f726bdaf92df78273217dfdd24ebcd6b0ef48cf3ce776ff5762b6e138c99'
readonly ZIZMOR_SHA256_AARCH64='37eebbd5762a44f262d94875431b14a6d2402991e90a685113ea97b8b41be833'
if [ "$(uname -m)" = 'aarch64' ]; then
  readonly ZIZMOR_SHA256="${ZIZMOR_SHA256_AARCH64}" ZIZMOR_ARCH='aarch64'
else
  readonly ZIZMOR_SHA256="${ZIZMOR_SHA256_X86_64}" ZIZMOR_ARCH='x86_64'
fi
readonly ZIZMOR_ASSET="zizmor-${ZIZMOR_ARCH}-unknown-linux-gnu.tar.gz"
readonly ZIZMOR_URL="https://github.com/zizmorcore/zizmor/releases/download/v${ZIZMOR_VERSION}/${ZIZMOR_ASSET}"
readonly ZIZMOR_INSTALL_PATH='/usr/local/bin/zizmor'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  if command -v zizmor >/dev/null 2>&1; then
    log "zizmor already installed: $(zizmor --version 2>&1 | head -1)"
    return 0
  fi
  case "$(uname -s)" in
    Linux)
      log "Installing zizmor v${ZIZMOR_VERSION} (Linux)"
      install_linux
      ;;
    *)
      log '❌ zizmor installs in the dev image (Linux); run its targets through make'
      exit 1
      ;;
  esac
  log "zizmor installed: $(zizmor --version 2>&1 | head -1)"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/setup_zizmor.log' >&5
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

function install_linux() {
  local tmp
  tmp="$(mktemp -d)"
  curl -fsSL --retry 5 --retry-delay 10 --retry-all-errors --connect-timeout 30 --speed-limit 1024 --speed-time 60 "${ZIZMOR_URL}" -o "${tmp}/${ZIZMOR_ASSET}"
  printf '%s  %s/%s\n' "${ZIZMOR_SHA256}" "${tmp}" "${ZIZMOR_ASSET}" | sha256sum --check --quiet
  stage_binary "${tmp}"
  as_root install -m 0755 "${tmp}/zizmor.staged" "${ZIZMOR_INSTALL_PATH}"
  rm -rf "${tmp}"
}

# stage_binary extracts the tarball and stages the zizmor binary wherever it sits in it.
function stage_binary() {
  local -r tmp="${1}"
  tar -xzf "${tmp}/${ZIZMOR_ASSET}" -C "${tmp}"
  find "${tmp}" -type f -name zizmor -perm -u+x -exec mv {} "${tmp}/zizmor.staged" \;
  [ -f "${tmp}/zizmor.staged" ] || { log "ERROR: no zizmor binary in ${ZIZMOR_ASSET}"; exit 1; }
}

main "${@:-}"
