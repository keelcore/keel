#!/usr/bin/env bash
# setup-kubeconform.sh
# Install kubeconform for offline Kubernetes schema validation.
# Linux: download binary from GitHub releases.
# macOS: Homebrew.
# No-op if kubeconform is already present.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly KUBECONFORM_VERSION='v0.6.7'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  if command -v kubeconform >/dev/null 2>&1; then
    log "kubeconform already installed: $(kubeconform -v 2>&1 | head -1)"
    return 0
  fi
  case "$(uname -s)" in
    Linux)
      log "Installing kubeconform ${KUBECONFORM_VERSION} (Linux)"
      install_linux
      ;;
    Darwin)
      log "Installing kubeconform via Homebrew"
      install_macos
      ;;
    *)
      log "ERROR: setup-kubeconform.sh does not support OS: $(uname -s)"
      exit 1
      ;;
  esac
  log "kubeconform installed: $(kubeconform -v 2>&1 | head -1)"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_setup_kubeconform.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected arg'
    exit 1
  fi
}

function install_linux() {
  local arch
  arch="$(uname -m)"
  case "${arch}" in
    x86_64)  arch='amd64' ;;
    aarch64) arch='arm64' ;;
    *)
      log "ERROR: unsupported arch: ${arch}"
      exit 1
      ;;
  esac
  local -r url="https://github.com/yannh/kubeconform/releases/download/${KUBECONFORM_VERSION}/kubeconform-linux-${arch}.tar.gz"
  curl -fsSL "${url}" | sudo tar -xzf - -C /usr/local/bin kubeconform
  sudo chmod +x /usr/local/bin/kubeconform
}

function install_macos() {
  brew install kubeconform
}

main "${@:-}"
