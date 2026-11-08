#!/usr/bin/env bash
# setup-helm.sh
# Install the pinned Helm. Upstream: https://github.com/helm/helm/releases/download (the binaries come
# from get.helm.sh, which that release page names).
# Linux: the release tarball from get.helm.sh, verified against the committed
# SHA-256 for the runner's architecture (same version and sums as the toolbox
# Containerfile; update-pins.local.sh keeps both in step).
# No-op if the pinned version is already present.
# Project hook: setup-helm.local.sh runs first, when present, with this script's arguments
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

readonly HELM_VERSION='v4.3.1'
readonly HELM_SHA256_AMD64='0403a88ae20913d11b10a2467b50087721fea7581736da6b9d3992f5357fe54b'
readonly HELM_SHA256_ARM64='8cf8f66ecbc252ac8f7f06cca68aba88508ddfde3a6a34feb1678ad1694b4f88'
readonly HELM_INSTALL_PATH='/usr/local/bin/helm'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  case "$(uname -s)" in
    Linux)
      install_helm_linux
      ;;
    *)
      log '❌ helm installs in the dev image (Linux); run its targets through make'
      exit 1
      ;;
  esac
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/setup_helm.log' >&5
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

function install_helm_linux() {
  if command -v helm >/dev/null 2>&1 && [[ "$(helm version --short)" == "${HELM_VERSION}+"* ]]; then
    log "✅ helm ${HELM_VERSION} already installed"
    return 0
  fi
  local arch sha tarball tmp
  case "$(uname -m)" in
    x86_64) arch='amd64'; sha="${HELM_SHA256_AMD64}" ;;
    aarch64) arch='arm64'; sha="${HELM_SHA256_ARM64}" ;;
    *) log "❌ Unsupported architecture: $(uname -m)"; exit 1 ;;
  esac
  tarball="helm-${HELM_VERSION}-linux-${arch}.tar.gz"
  tmp="$(mktemp -d)"
  log "⚓ Installing helm ${HELM_VERSION} (${arch})..."
  curl -fsSL --retry 5 --retry-delay 10 --retry-all-errors --connect-timeout 30 --speed-limit 1024 --speed-time 60 "https://get.helm.sh/${tarball}" -o "${tmp}/${tarball}"
  printf '%s  %s/%s\n' "${sha}" "${tmp}" "${tarball}" | sha256sum --check --quiet
  tar -xz -C "${tmp}" -f "${tmp}/${tarball}"
  as_root install -m 0755 "${tmp}/linux-${arch}/helm" "${HELM_INSTALL_PATH}"
  rm -rf "${tmp}"
  log "✅ helm ${HELM_VERSION} installed at ${HELM_INSTALL_PATH}"
}

main "${@:-}"
