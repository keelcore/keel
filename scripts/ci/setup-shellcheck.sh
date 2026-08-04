#!/usr/bin/env bash
# setup-shellcheck.sh
# Install shellcheck on a CI runner.
# No-op if shellcheck is already present.

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
  install_shellcheck
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/setup_shellcheck.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

function install_shellcheck() {
  if command -v shellcheck > /dev/null 2>&1; then
    log "✅ shellcheck already installed ($(shellcheck --version | head -2 | tail -1))"
    return 0
  fi
  if [[ "$(uname -s)" != 'Linux' ]]; then
    log '❌ shellcheck not found. Install via: brew install shellcheck'
    exit 1
  fi
  log '⚓ Installing shellcheck...'
  sudo apt-get install -y shellcheck
  log '✅ shellcheck installed'
}

main "${@:-}"
