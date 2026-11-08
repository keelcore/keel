#!/usr/bin/env bash
# setup-kubescape.sh
# Install kubescape for the Helm chart policy scan (scripts/lint/lint-helm-policy.sh).
# Linux (built into the dev image): pinned v4.0.16 release asset, SHA256-verified before install.
# No-op if kubescape is already present.
#
# Pinned version: v4.0.16
# Update KUBESCAPE_VERSION and KUBESCAPE_SHA256_* together when bumping (make update-pins does).
# Project hook: setup-kubescape.local.sh runs first, when present, with this script's arguments
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

readonly KUBESCAPE_VERSION='4.0.16'
readonly KUBESCAPE_SHA256_AMD64='fcf172f0d1fb5683379586bcf8875091da4d0d815bd304f8098d58be65ecab0d'
readonly KUBESCAPE_SHA256_ARM64='ea0a5c51d299e88209e2f17877dd76f66d36b772cf313e8fe68ee37325c9b382'
if [ "$(uname -m)" = 'aarch64' ]; then
  readonly KUBESCAPE_SHA256="${KUBESCAPE_SHA256_ARM64}" KUBESCAPE_ARCH='arm64'
else
  readonly KUBESCAPE_SHA256="${KUBESCAPE_SHA256_AMD64}" KUBESCAPE_ARCH='amd64'
fi
readonly KUBESCAPE_ASSET="kubescape_${KUBESCAPE_VERSION}_linux_${KUBESCAPE_ARCH}"
readonly KUBESCAPE_URL="https://github.com/kubescape/kubescape/releases/download/v${KUBESCAPE_VERSION}/${KUBESCAPE_ASSET}"
readonly KUBESCAPE_INSTALL_PATH='/usr/local/bin/kubescape'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  if command -v kubescape >/dev/null 2>&1; then
    log "kubescape already installed: $(kubescape version 2>&1 | head -1)"
    return 0
  fi
  case "$(uname -s)" in
    Linux)
      log "Installing kubescape v${KUBESCAPE_VERSION} (Linux)"
      install_linux
      ;;
    *)
      log '❌ kubescape installs in the dev image (Linux); run its targets through make'
      exit 1
      ;;
  esac
  log "kubescape installed: $(kubescape version 2>&1 | head -1)"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/setup_kubescape.log' >&5
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
  curl -fsSL --retry 5 --retry-delay 10 --retry-all-errors --connect-timeout 30 --speed-limit 1024 --speed-time 60 "${KUBESCAPE_URL}" -o "${tmp}/${KUBESCAPE_ASSET}"
  printf '%s  %s/%s\n' "${KUBESCAPE_SHA256}" "${tmp}" "${KUBESCAPE_ASSET}" | sha256sum --check --quiet
  stage_binary "${tmp}"
  as_root install -m 0755 "${tmp}/kubescape.staged" "${KUBESCAPE_INSTALL_PATH}"
  rm -rf "${tmp}"
}

# stage_binary stages the release asset, which is the kubescape binary itself.
function stage_binary() {
  local -r tmp="${1}"
  mv "${tmp}/${KUBESCAPE_ASSET}" "${tmp}/kubescape.staged"
}

main "${@:-}"
