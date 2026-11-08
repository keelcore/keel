#!/usr/bin/env bash
# generate-coverage-profile.sh
# Generates a coverage profile and prints total %, uncovered statements, and total statements.
# Project hook: generate-coverage-profile.local.sh runs first, when present, with this script's arguments
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
# shellcheck source=../lib/tool-config.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/tool-config.sh"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  local outfile
  outfile="${1:-target/coverage.txt}"
  run_coverage "${outfile}"
  print_stats "${outfile}"
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/go_coverage.log' >&5
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
  local pkgs coverpkg raw
  # config/go-test-exclude.local.txt drops packages from the run; config/go-coverpkg.local.txt narrows
  # what counts as covered (regexes, one per line). Files named main.go never count.
  pkgs="$(go_pkgs | filter_out "$(config_list go-test-exclude)")"
  coverpkg="$(printf '%s\n' "${pkgs}" | keep_only "$(config_list go-coverpkg)" | paste -sd, -)"
  raw="${outfile}.raw"
  printf '%s\n' "${pkgs}" | xargs go test \
    -count=1 \
    -coverprofile="${raw}" \
    -covermode=atomic \
    "-coverpkg=${coverpkg}"
  grep -v '/main\.go:' "${raw}" > "${outfile}"
  rm -f "${raw}"
}

# filter_out PATTERNS, keep_only PATTERNS: stdin lines without, or with, a match of a line of PATTERNS.
function filter_out() {
  if [ -z "${1}" ]; then cat; else grep -vEf <(printf '%s\n' "${1}") || true; fi
}

function keep_only() {
  if [ -z "${1}" ]; then cat; else grep -Ef <(printf '%s\n' "${1}") || true; fi
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
