#!/usr/bin/env bash
# setup-pebble.sh
# Install the pebble RFC 8555 test CA on a CI runner.
# No-op if pebble is already present.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly PEBBLE_MODULE='github.com/letsencrypt/pebble/cmd/pebble@v1.0.1'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  if command -v pebble >/dev/null 2>&1; then
    log "pebble already installed: $(pebble --version 2>&1 || true)"
    return 0
  fi
  install_pebble
  log "✅ pebble installed"
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_setup_pebble.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected arg'
    exit 1
  fi
}

function install_pebble() {
  log "Installing pebble via go install ${PEBBLE_MODULE}"
  go install "${PEBBLE_MODULE}"
}

main "${@:-}"
