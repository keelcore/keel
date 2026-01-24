#!/usr/bin/env bash
# ci.sh
# Executes unit tests and generates coverage reports for Keel.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

function main() {
  exec 5>&1
  local -r log_file='/tmp/keel_test.log'
  validate_args "${@:-}"
  log "🧪 Initializing test suite"
  run_unit_tests
  log "🎉 All tests passed successfully"
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_test.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 0 ] && [ -z "${1:-}" ]; then
    log "❌ Error: Unexpected empty argument"
    exit 1
  fi
}

function run_unit_tests() {
  log "🏃 Running Go tests with race detection"
  invoke_go_test
}

function invoke_go_test() {
  go test -v -race \
    -coverprofile='coverage.txt' \
    -covermode='atomic' ./...
}

main "${@:-}"
