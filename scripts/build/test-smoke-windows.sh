#!/usr/bin/env bash
# test-smoke-windows.sh
# Build the no-FIPS keel binary on a Windows CI runner and verify it starts.
# Must be invoked with shell: bash (Git Bash / MSYS2) in GitHub Actions.

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
  require_binary
  log "Running smoke test: dist/keel-max.exe --version"
  smoke_test
  log "Windows smoke test passed"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_smoke_windows.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

# require_binary: dist/keel-max.exe, cross-built in the dev image (make build-native
# OS=windows ARCH=amd64); the Windows host only runs it.
function require_binary() {
  [ -f 'dist/keel-max.exe' ] && return 0
  log '❌ dist/keel-max.exe not found (make build-native OS=windows ARCH=amd64)'
  exit 1
}

function smoke_test() {
  './dist/keel-max.exe' --version
}

main "${@:-}"
