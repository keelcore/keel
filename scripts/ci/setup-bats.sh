#!/usr/bin/env bash
# setup-bats.sh
# Install bats-core on a CI runner. Supports Linux (apt) and macOS (brew).
# No-op if bats is already present.

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
  if command -v bats >/dev/null 2>&1; then
    log "bats already installed: $(bats --version)"
    return 0
  fi
  install_bats
  log "bats installed: $(bats --version)"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_setup_bats.log' >&5
}

function validate_args() { :; }

function install_bats() {
  case "$(uname -s)" in
    Linux)  apt_install_bats  ;;
    Darwin) brew_install_bats ;;
    *)
      log "ERROR: setup-bats.sh does not support this platform: $(uname -s)"
      exit 1
      ;;
  esac
}

function apt_install_bats() {
  log "Installing bats-core via apt"
  sudo apt-get update -qq
  sudo apt-get install -y bats
}

function brew_install_bats() {
  log "Installing bats-core via brew"
  brew install bats-core
}

main "${@:-}"