#!/usr/bin/env bash
# ci_smoke_windows.sh
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
  log "Building keel on Windows"
  build_binary
  rename_for_windows
  log "Running smoke test: dist/keel-max.exe --version"
  smoke_test
  log "Windows smoke test passed"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_smoke_windows.log' >&5
}

function validate_args() { :; }

function build_binary() {
  bash scripts/build/ci_max_no_fips.sh
}

function rename_for_windows() {
  mv 'dist/keel-max' 'dist/keel-max.exe'
}

function smoke_test() {
  './dist/keel-max.exe' --version
}

main "${@:-}"
