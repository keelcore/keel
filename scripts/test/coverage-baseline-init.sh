#!/usr/bin/env bash
# coverage-baseline-init.sh
# Operator one-shot: capture the current LCOV as the coverage baseline.
# Per `.standards/governance/testing.md` v1.1.0+ "Coverage Regression
# Policy": initialization is EXEMPT from the 95% new-file floor — every
# file in the source LCOV becomes a baseline entry at its current ratio.
# The rule (no-regression + 95% new-file floor) binds from the SECOND
# change set onward.
#
# Run this once per repo, then again only after an approved change set
# that increases coverage (and the increased numbers should become the
# new baseline).
#
# Canonical (shipped). Invoked as `make coverage-baseline-init`.
#
# Usage:
#   bash coverage-baseline-init.sh [--source PATH] [--target PATH]
#
#   --source PATH  defaults to ${REPO_ROOT}/target/llvm-cov/lcov.info
#                  if missing, runs `make coverage` to generate it
#   --target PATH  defaults to ${REPO_ROOT}/tests/coverage-baseline.lcov

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

declare REPO_ROOT
REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
declare -r REPO_ROOT

declare SOURCE="${REPO_ROOT}/target/coverage/lcov.info"
declare TARGET="${REPO_ROOT}/tests/coverage-baseline.lcov"

function main() {
  exec 5>&1
  parse_args "${@:-}"
  ensure_source
  install_baseline
}

function parse_args() {
  while [ "${#}" -gt 0 ]; do
    case "${1:-}" in
      --source) SOURCE="${2:-}"; shift 2 ;;
      --target) TARGET="${2:-}"; shift 2 ;;
      '') shift ;;
      *)
        log "❌ unknown argument: ${1}"
        exit 2
        ;;
    esac
  done
}

function ensure_source() {
  if [ -f "${SOURCE}" ]; then
    return 0
  fi
  log "ℹ️  source LCOV missing at ${SOURCE}; running 'make coverage'..."
  make -C "${REPO_ROOT}" coverage >&2
  if [ ! -f "${SOURCE}" ]; then
    log "❌ coverage did not produce ${SOURCE}"
    exit 2
  fi
}

function install_baseline() {
  mkdir -p "$(dirname "${TARGET}")"
  cp "${SOURCE}" "${TARGET}"
  log "✅ baseline captured: ${TARGET}"
  log "   commit this file; from the next change set forward, every"
  log "   per-file ratio must hold (existing) or be >= 0.95 (new)."
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/coverage_baseline_init.log' >&5
}

main "${@:-}"
