#!/usr/bin/env bash
# setup-kubeconform.sh
# Install kubeconform for offline Kubernetes schema validation.
# Linux: download the release tarball and verify its committed SHA-256.
# No-op if kubeconform is already present.
# Project hook: setup-kubeconform.local.sh runs first, when present, with this script's arguments
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

readonly KUBECONFORM_VERSION='v0.8.0'
# Published in the release's CHECKSUMS file.
readonly KUBECONFORM_SHA256_AMD64='9bc2bffbf71f261128533edaf912153948b7ff238f9a531ae6d34466ec287883'
readonly KUBECONFORM_SHA256_ARM64='1f53fc8e81258197a35e8603054162a5af1de8c5af13746c71ab680d9534ed87'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  if command -v kubeconform >/dev/null 2>&1; then
    log "kubeconform already installed: $(kubeconform -v 2>&1 | head -1)"
    return 0
  fi
  case "$(uname -s)" in
    Linux)
      log "Installing kubeconform ${KUBECONFORM_VERSION} (Linux)"
      install_linux
      ;;
    *)
      log '❌ kubeconform installs in the dev image (Linux); run its targets through make'
      exit 1
      ;;
  esac
  log "kubeconform installed: $(kubeconform -v 2>&1 | head -1)"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/setup_kubeconform.log' >&5
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
  local arch sum
  arch="$(uname -m)"
  case "${arch}" in
    x86_64)  arch='amd64'; sum="${KUBECONFORM_SHA256_AMD64}" ;;
    aarch64) arch='arm64'; sum="${KUBECONFORM_SHA256_ARM64}" ;;
    *)
      log "ERROR: unsupported arch: ${arch}"
      exit 1
      ;;
  esac
  local -r url="https://github.com/yannh/kubeconform/releases/download/${KUBECONFORM_VERSION}/kubeconform-linux-${arch}.tar.gz"
  local tmp
  tmp="$(mktemp -d)"
  curl -fsSL --retry 5 --retry-delay 10 --retry-all-errors --connect-timeout 30 --speed-limit 1024 --speed-time 60 "${url}" -o "${tmp}/kubeconform.tar.gz"
  printf '%s  %s\n' "${sum}" "${tmp}/kubeconform.tar.gz" | sha256sum --check --quiet
  as_root tar -xzf "${tmp}/kubeconform.tar.gz" -C /usr/local/bin kubeconform
  as_root chmod +x /usr/local/bin/kubeconform
  rm -rf "${tmp}"
}

main "${@:-}"
