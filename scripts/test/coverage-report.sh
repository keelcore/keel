#!/usr/bin/env bash
# coverage-report.sh
# Pretty-print the merged per-file LCOV (target/coverage/lcov.info) as an
# ascending, colorized console table with a coverage bar. Read-only: consumes
# whatever `make coverage` last produced; does not run tests.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

declare -r LCOV="${COVERAGE_LCOV:-target/coverage/lcov.info}"

function main() {
  validate_args "${@:-}"
  if [ ! -f "${LCOV}" ]; then
    printf '❌ no LCOV at %s — run `make coverage` first.\n' "${LCOV}" >&2
    exit 1
  fi
  render
}

function render() {
  awk -F: '
    /^SF:/{f=$2} /^LF:/{lf=$2} /^LH:/{lh=$2}
    /^end_of_record/{pct=(lf>0)?100*lh/lf:0; printf "%.1f\t%d\t%d\t%s\n", pct, lh, lf, f}
  ' "${LCOV}" | sort -n | awk -F'\t' '
    BEGIN{
      printf "\n  %-6s  %-22s  %-9s  %s\n", "COV", "COVERAGE BAR", "LINES", "FILE"
      printf "  %s\n", "─────────────────────────────────────────────────────────────────────────"
    }
    {
      pct=$1; lh=$2; lf=$3; file=$4
      filled=int(pct/5+0.5); bar=""
      for(i=0;i<20;i++) bar=bar (i<filled ? "\342\226\210" : "\302\267")
      c = (pct==0) ? "\033[90m" : (pct<80 ? "\033[31m" : (pct<95 ? "\033[33m" : "\033[32m"))
      printf "  %s%5.1f%%\033[0m  %s%s\033[0m  %5d/%-5d  %s\n", c, pct, c, bar, lh, lf, file
    }
    END{ printf "  %s\n", "─────────────────────────────────────────────────────────────────────────" }
  '
  awk -F: '/^LF:/{lf+=$2}/^LH:/{lh+=$2}END{printf "  \033[1mTOTAL\033[0m   %.1f%%  %d/%d lines\n\n", (lf>0)?100*lh/lf:0, lh, lf}' "${LCOV}"
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    printf '❌ Error: Unexpected argument\n' >&2
    exit 1
  fi
}

main "${@:-}"
