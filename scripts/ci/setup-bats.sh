#!/usr/bin/env bash
# setup-bats.sh
# Install a pinned bats-core on a Linux CI runner via apt.
# No-op if bats is already present. Linux only.
#
# Pinned to the bats-core package available in the Ubuntu apt repository.
# For a specific version pin, use bats-core/bats-core releases directly.

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
  require_linux
  log "Installing bats-core via apt"
  apt_install_bats
  log "bats installed: $(bats --version)"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_setup_bats.log' >&5
}

function validate_args() { :; }

function require_linux() {
  if [[ "$(uname -s)" != 'Linux' ]]; then
    log "ERROR: setup-bats.sh only supports Linux CI runners"
    log "  For macOS: brew install bats-core"
    exit 1
  fi
}

function apt_install_bats() {
  sudo apt-get update -qq
  sudo apt-get install -y bats
}

main "${@:-}"