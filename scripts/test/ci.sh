#!/usr/bin/env bash
# ci.sh
# Executes unit tests via gotestsum and generates JUnit XML + coverage reports.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/paths.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"

readonly GOTESTSUM_VERSION='v1.13.0'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  log "Initializing test suite"
  ensure_gotestsum
  run_unit_tests
  write_step_summary
  log "All tests passed"
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_test.log' >&5
}

function validate_args() { :; }

function ensure_gotestsum() {
  if ! command -v gotestsum >/dev/null 2>&1; then
    log "gotestsum not found; installing ${GOTESTSUM_VERSION}"
    go install "gotest.tools/gotestsum@${GOTESTSUM_VERSION}"
  fi
}

function run_unit_tests() {
  log "Running Go tests with race detection"
  local pkgs coverpkg
  pkgs="$(go_pkgs)"
  coverpkg="$(printf '%s\n' "${pkgs}" | tr '\n' ',' | sed 's/,$//')"
  printf '%s\n' "${pkgs}" | xargs gotestsum \
    --junitfile test-results.xml \
    --format standard-verbose \
    -- -race -coverprofile='coverage.txt' -covermode='atomic' \
    "-coverpkg=${coverpkg}"
}

function write_step_summary() {
  [ -z "${GITHUB_STEP_SUMMARY:-}" ] && return 0
  printf '## Unit Test Results\n\n' >> "${GITHUB_STEP_SUMMARY}"
  printf '| Result | Count |\n|---|---|\n' >> "${GITHUB_STEP_SUMMARY}"
  summarise_junit >> "${GITHUB_STEP_SUMMARY}"
}

function summarise_junit() {
  local passed failed skipped
  passed="$(grep -c 'status="passed"' test-results.xml 2>/dev/null || printf '0')"
  failed="$(grep -c 'status="failed"' test-results.xml 2>/dev/null || printf '0')"
  skipped="$(grep -c 'status="skipped"' test-results.xml 2>/dev/null || printf '0')"
  printf '| Passed | %s |\n| Failed | %s |\n| Skipped | %s |\n' \
    "${passed}" "${failed}" "${skipped}"
}

main "${@:-}"