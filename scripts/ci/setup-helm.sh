#!/usr/bin/env bash
# setup-helm.sh
# Install Helm via the platform-appropriate package manager.
# Linux: official GPG-signed apt repository.
# macOS: Homebrew.
# No-op if helm is already present.

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
  case "$(uname -s)" in
    Linux)
      log "Installing Helm via official apt repository"
      install_helm_apt
      ;;
    Darwin)
      log "Installing Helm via Homebrew"
      install_helm_brew
      ;;
    *)
      log "ERROR: setup-helm.sh does not support OS: $(uname -s)"
      exit 1
      ;;
  esac
  log "Helm installed: $(helm version --short)"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_setup_helm.log' >&5
}

function validate_args() { :; }

function install_helm_brew() {
  brew install helm
}

function install_helm_apt() {
  add_helm_gpg_key
  add_helm_apt_repo
  apt_install_helm
}

function add_helm_gpg_key() {
  curl -fsSL 'https://baltocdn.com/helm/signing.asc' \
    | gpg --dearmor \
    | sudo tee /usr/share/keyrings/helm.gpg >/dev/null
  sudo apt-get install -y apt-transport-https --quiet
}

function add_helm_apt_repo() {
  printf 'deb [arch=%s signed-by=/usr/share/keyrings/helm.gpg] https://baltocdn.com/helm/stable/debian/ all main\n' \
    "$(dpkg --print-architecture)" \
    | sudo tee /etc/apt/sources.list.d/helm-stable-debian.list
  sudo apt-get update -qq
}

function apt_install_helm() {
  sudo apt-get install -y helm
}

main "${@:-}"
