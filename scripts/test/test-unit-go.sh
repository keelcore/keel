#!/usr/bin/env bash
# test-unit-go.sh
# Executes unit tests via gotestsum and generates JUnit XML + coverage reports.
# Environment: GITHUB_STEP_SUMMARY (default empty).
# Project hook: test-unit-go.local.sh runs first, when present, with this script's arguments
# (scripts/lib/local-hook.sh: the same path, in the repository this runs in).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/local-hook.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/local-hook.sh"

# shellcheck source=../lib/paths.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"

declare -rx GITHUB_STEP_SUMMARY="${GITHUB_STEP_SUMMARY:-}"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  if [ -z "$(source_files '*go.mod')" ]; then
    log 'ℹ️  No Go module; nothing to test'
    return 0
  fi
  log "Initializing test suite"
  ensure_gotestsum
  run_unit_tests
  generate_coverage
  write_step_summary
  log "All tests passed"
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/go_test.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected arg'
    exit 1
  fi
}

function ensure_gotestsum() {
  command -v gotestsum >/dev/null 2>&1 && return 0
  log '❌ gotestsum not found (the dev image carries it; native runners: make setup-gotestsum)'
  exit 1
}

function run_unit_tests() {
  log "Running Go tests with race detection"
  mkdir -p target
  local pkgs
  pkgs="$(go_pkgs)"
  printf '%s\n' "${pkgs}" | xargs gotestsum \
    --junitfile target/test-results.xml \
    --format standard-verbose \
    -- -race
}

function generate_coverage() {
  log "Generating coverage report"
  "$(dirname "${BASH_SOURCE[0]}")/generate-coverage-profile.sh" 'target/coverage.txt'
}

function write_step_summary() {
  [ -z "${GITHUB_STEP_SUMMARY}" ] && return 0
  printf '## Unit Test Results\n\n' >> "${GITHUB_STEP_SUMMARY}"
  printf '| Result | Count |\n|---|---|\n' >> "${GITHUB_STEP_SUMMARY}"
  summarise_junit >> "${GITHUB_STEP_SUMMARY}"
}

function summarise_junit() {
  local total failed skipped passed
  total="$(grep -c '<testcase ' target/test-results.xml 2>/dev/null || true)"
  failed="$(grep -c '<failure' target/test-results.xml 2>/dev/null || true)"
  skipped="$(grep -c '<skipped' target/test-results.xml 2>/dev/null || true)"
  passed="$(( ${total:-0} - ${failed:-0} - ${skipped:-0} ))"
  printf '| Passed | %s |\n| Failed | %s |\n| Skipped | %s |\n' \
    "${passed}" "${failed}" "${skipped}"
}

main "${@:-}"
