#!/usr/bin/env bash
# ci_test_binary.sh
# Builds the minimalist keel binary then runs the BATS integrity suite.

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
  log '🔨 Building minimalist binary for integrity testing...'
  build_min
  log '🔨 Building max (full-feature) binary for integrity testing...'
  build_max
  log '🧪 Running BATS integrity suite...'
  run_bats
  log '✅ Integrity suite passed'
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_ci_test_binary.log' >&5
}

function validate_args() { :; }

function build_min() {
  ./scripts/build/ci_min.sh
}

function build_max() {
  ./scripts/build/ci_max_no_fips.sh
}

function run_bats() {
  if ! command -v bats > /dev/null 2>&1; then
    log '❌ bats not found — install bats-core (brew install bats-core)'
    exit 1
  fi
  bats tests/integrity.bats
}

main "${@:-}"
