#!/usr/bin/env bash
# coverage.sh
# Generates a coverage profile and prints total %, uncovered statements, and total statements.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/paths.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local outfile
  outfile="${1:-coverage.txt}"
  run_coverage "${outfile}"
  print_stats "${outfile}"
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_coverage.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ]; then
    log '❌ Error: Unexpected arg'
    exit 1
  fi
}

function run_coverage() {
  local outfile="${1}"
  log "Generating coverage profile"
  : > "${outfile}"
  local pkgs coverpkg
  pkgs="$(go_pkgs | grep -v '/examples/')"
  coverpkg="$(printf '%s\n' "${pkgs}" | tr '\n' ',' | sed 's/,$//')"
  printf '%s\n' "${pkgs}" | xargs go test \
    -count=1 \
    -coverprofile="${outfile}" \
    -covermode=atomic \
    "-coverpkg=${coverpkg}"
}

function print_stats() {
  local file="${1}"
  local pct total_lines uncovered
  pct="$(go tool cover -func="${file}" | grep '^total:' | awk '{print $3}')"
  read -r total_lines uncovered < <(
    awk '
      NR==1 { next }
      { cnt[$1]+=$3; stmts[$1]=$2 }
      END {
        for (b in stmts) { tot+=stmts[b]; if (cnt[b]==0) unc+=stmts[b] }
        print tot+0, unc+0
      }
    ' "${file}"
  )
  printf 'total:     %s\n' "${pct}"
  printf 'uncovered: %s\n' "${uncovered}"
  printf 'lines:     %s\n' "${total_lines}"
}

main "${@:-}"
