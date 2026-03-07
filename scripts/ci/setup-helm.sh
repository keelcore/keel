#!/usr/bin/env bash
# setup-helm.sh
# Install Helm on a Linux CI runner via the official GPG-signed apt repository.
# No-op if helm is already present. Linux only.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

function main() {
  exec 5>&1
  validate_args "${@:-}"
  if command -v helm >/dev/null 2>&1; then
    log "helm already installed: $(helm version --short)"
    return 0
  fi
  require_linux
  log "Installing Helm via official apt repository"
  install_helm_apt
  log "Helm installed: $(helm version --short)"
}

function log() {
  printf '%s\n' "${1:-}" >&5
}

function validate_args() { :; }

function require_linux() {
  if [[ "$(uname -s)" != 'Linux' ]]; then
    printf 'ERROR: setup-helm.sh only supports Linux CI runners\n' >&2
    printf '  For macOS: brew install helm\n' >&2
    printf '  For Windows: choco install kubernetes-helm\n' >&2
    exit 1
  fi
}

function install_helm_apt() {
  curl -fsSL 'https://baltocdn.com/helm/signing.asc' \
    | gpg --dearmor \
    | sudo tee /usr/share/keyrings/helm.gpg >/dev/null

  sudo apt-get install -y apt-transport-https --quiet

  printf 'deb [arch=%s signed-by=/usr/share/keyrings/helm.gpg] https://baltocdn.com/helm/stable/debian/ all main\n' \
    "$(dpkg --print-architecture)" \
    | sudo tee /etc/apt/sources.list.d/helm-stable-debian.list

  sudo apt-get update -qq
  sudo apt-get install -y helm
}

main "${@:-}"