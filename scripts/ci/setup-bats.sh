#!/usr/bin/env bash
# scripts/test/setup-bats.sh
# Install bats-core on a developer machine or CI runner.
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
  install_bats
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/setup_bats.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

function install_bats() {
  if command -v bats > /dev/null 2>&1; then
    log "✅ bats already installed ($(bats --version))"
    return 0
  fi
  case "$(uname -s)" in
    Darwin)
      log '⚓ Installing bats via brew...'
      brew install bats-core
      ;;
    Linux)
      log '⚓ Installing bats via apt-get...'
      sudo apt-get install -y bats
      ;;
    *)
      log "❌ Unsupported OS: $(uname -s). Install bats manually: https://bats-core.readthedocs.io/"
      exit 1
      ;;
  esac
  log '✅ bats installed'
}

main "${@:-}"
