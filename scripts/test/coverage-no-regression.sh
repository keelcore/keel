#!/usr/bin/env bash
# coverage-no-regression.sh
# Per-file coverage gate. Implements `.standards/governance/testing.md`
# "Coverage Regression Policy" (v1.1.0+):
#   - For each SF: present in both baseline and current LCOV, asserts
#     current LH/LF >= baseline LH/LF (no-regression on existing files).
#   - For each SF: present only in current (new file), asserts
#     LH/LF >= 0.95 (new-file 95% floor; unwaivable).
#   - For each SF: present only in baseline (file removed), flags the
#     stale baseline entry — operator must drop it from baseline in the
#     same change set (or restore the file).
#
# Exit semantics:
#   0  — no regressions, no floor violations, no stale entries
#   1  — at least one finding (regression, floor violation, or stale entry)
#   2  — hard error (bad args, missing baseline, malformed LCOV)
#
# Canonical (shipped to consumers via bootstrap-standards.sh). CI invokes
# it as `make ci-coverage-no-regression`.
#
# Usage:
#   bash coverage-no-regression.sh [--baseline PATH] [--current PATH]
#
#   --baseline PATH   defaults to ${REPO_ROOT}/tests/coverage-baseline.lcov
#   --current PATH    defaults to ${REPO_ROOT}/target/coverage/lcov.info

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

declare BASELINE="${REPO_ROOT}/tests/coverage-baseline.lcov"
declare CURRENT="${REPO_ROOT}/target/coverage/lcov.info"

function main() {
  exec 5>&1
  parse_args "${@:-}"
  validate_files
  run_comparison
}

function parse_args() {
  while [ "${#}" -gt 0 ]; do
    case "${1:-}" in
      --baseline) BASELINE="${2:-}"; shift 2 ;;
      --current)  CURRENT="${2:-}";  shift 2 ;;
      '') shift ;;
      *)
        log "❌ unknown argument: ${1}"
        exit 2
        ;;
    esac
  done
}

function validate_files() {
  if [ ! -f "${BASELINE}" ]; then
    log "❌ coverage-no-regression: baseline missing at ${BASELINE}"
    log "   Run 'make coverage-baseline-init' once to establish it,"
    log "   then commit the resulting tests/coverage-baseline.lcov."
    exit 2
  fi
  if [ ! -f "${CURRENT}" ]; then
    log "❌ coverage-no-regression: current LCOV missing at ${CURRENT}"
    log "   Run 'make coverage' first."
    exit 2
  fi
}

function run_comparison() {
  awk -v floor='0.95' -v eps='0.0001' '
    NR == FNR && /^SF:/ { bsf = substr($0, 4); seen_b[bsf] = 1; next }
    NR == FNR && /^LF:/ { blf[bsf] = substr($0, 4) + 0; next }
    NR == FNR && /^LH:/ { blh[bsf] = substr($0, 4) + 0; next }
    NR > FNR  && /^SF:/ { csf = substr($0, 4); seen_c[csf] = 1; next }
    NR > FNR  && /^LF:/ { clf[csf] = substr($0, 4) + 0; next }
    NR > FNR  && /^LH:/ { clh[csf] = substr($0, 4) + 0; next }
    END {
      failed = 0
      for (sf in seen_b) {
        if (!(sf in seen_c)) {
          printf "⚠️  stale baseline entry (file no longer present): %s\n", sf
          printf "    remove from baseline in the same change set, or restore the file\n"
          failed = 1
          continue
        }
        base = (blf[sf] > 0) ? (blh[sf] / blf[sf]) : 1.0
        curr = (clf[sf] > 0) ? (clh[sf] / clf[sf]) : 1.0
        if (curr + eps < base) {
          printf "❌ regression on %s: %.4f < baseline %.4f\n", sf, curr, base
          failed = 1
        }
      }
      for (sf in seen_c) {
        if (sf in seen_b) continue
        curr = (clf[sf] > 0) ? (clh[sf] / clf[sf]) : 1.0
        if (curr + eps < floor) {
          printf "❌ new file %s below 95%% floor: %.4f\n", sf, curr
          failed = 1
        }
      }
      if (failed == 0) {
        printf "✅ coverage-no-regression: every per-file ratio meets baseline and 95%% new-file floor\n"
      }
      exit failed
    }
  ' "${BASELINE}" "${CURRENT}"
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/coverage_no_regression.log' >&5
}

main "${@:-}"
