#!/usr/bin/env bash
# scripts/ci/setup-bats.local.sh
# keel-specific extension of the canonical scripts/ci/setup-bats.sh, which runs
# this file after installing bats. The integrity suite bounds
# process runs with timeout(1); Linux runners ship it, macOS does not.
# Homebrew's coreutils installs timeout unprefixed. No-op when already present.

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
  install_timeout
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

function install_timeout() {
  if command -v timeout > /dev/null 2>&1; then
    log '✅ timeout already installed'
    return 0
  fi
  case "$(uname -s)" in
    Darwin)
      log '⚓ Installing coreutils (timeout) via brew...'
      brew install coreutils
      ;;
    *)
      log "❌ timeout not found on $(uname -s). Install GNU coreutils."
      exit 1
      ;;
  esac
  log '✅ timeout installed'
}

main "${@:-}"
