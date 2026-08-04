#!/usr/bin/env bash
# coverage-aggregate.sh
# Merge every per-language LCOV tracefile in the coverage bucket into one report
# and print coverage bucketed by source-file extension (the language rollup).
# LCOV records are self-contained and keyed by source path, so merging disjoint
# per-language tracefiles is a concatenation (Linux `lcov -a` sums overlaps; ours
# are disjoint). The merged file is what coverage-baseline-init snapshots and
# coverage-no-regression gates against.
#
# Input:  ${COVERAGE_DIR:-target/coverage}/*.lcov  (one per language)
# Output: ${COVERAGE_DIR}/lcov.info                (merged) + a per-language table

set -o nounset
set -o errexit
set -o pipefail

readonly COVERAGE_DIR="${COVERAGE_DIR:-target/coverage}"
readonly MERGED="${COVERAGE_DIR}/lcov.info"

function log() {
  printf '%s\n' "${1:-}" | tee -a '/tmp/keel_coverage_aggregate.log' >&5
}

function main() {
  exec 5>&1
  if ! ls "${COVERAGE_DIR}"/*.lcov >/dev/null 2>&1; then
    log 'ℹ️  no per-language LCOV tracefiles found; run make coverage-go / coverage-rust first.'
    return 0
  fi
  : > "${MERGED}"
  local f
  for f in "${COVERAGE_DIR}"/*.lcov; do
    [ "${f}" = "${MERGED}" ] && continue
    cat "${f}" >> "${MERGED}"
  done
  report
}

# report: bucket LCOV records by the SF: file extension (the language) and print
# per-language line coverage plus the overall total.
function report() {
  log '📊 Coverage by language (bucketed by SF file extension):'
  awk '
    /^SF:/  { p = substr($0, 4); sub(/.*\./, "", p); cur = p }
    /^LF:/  { lf[cur] += substr($0, 4); tlf += substr($0, 4) }
    /^LH:/  { lh[cur] += substr($0, 4); tlh += substr($0, 4) }
    END {
      for (e in lf) printf "   %-8s %6.1f%%  (%d/%d lines)\n", "." e, (lf[e] ? 100 * lh[e] / lf[e] : 0), lh[e], lf[e]
      printf "   %-8s %6.1f%%  (%d/%d lines)\n", "TOTAL", (tlf ? 100 * tlh / tlf : 0), tlh, tlf
    }
  ' "${MERGED}" >&5
  log "📄 merged LCOV -> ${MERGED}"
}

main "${@:-}"
