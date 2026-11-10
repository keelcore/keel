#!/usr/bin/env bash
# scripts/lint/lint-test-layout.local.sh
# keel's unit-test layout check (make lint-test-layout, part of make lint and the commit hook, #40): all unit
# tests for {foo}.go are in {foo}_test.go, in the same directory. Fails, naming each file, on:
#   - a test file with no {foo}.go beside it. A tag-named test file, {foo}_{tag}_test.go or
#     {foo}_not{tag}_test.go (any run of tag words), pairs with {foo}.go when it has a //go:build line;
#   - a source file with no {foo}_test.go beside it, except main.go (exercised through the built binary) and
#     doc.go (no code).
# Paths matching a line of config/test-layout.local.txt (extended regexes, # comments) are not checked.
# Project-owned, never canonical.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/paths.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"

readonly EXCLUDE_LIST='config/test-layout.local.txt'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  log '🔍 Unit-test layout: tests for {foo}.go in {foo}_test.go...'
  local failed=0 f
  while IFS= read -r f; do
    [ -f "${f}" ] || continue
    check_file "${f}" || failed=1
  done < <(source_files '*.go' | excluded_out)
  if [ "${failed}" -ne 0 ]; then
    log '❌ Unit-test layout check failed'
    exit 1
  fi
  log '✅ Unit-test layout: every test file has its source, every source its test file'
}

# excluded_out: stdin without the paths config/test-layout.local.txt lists.
function excluded_out() {
  local re=''
  [ ! -f "${EXCLUDE_LIST}" ] || re="$(grep -vE '^[[:space:]]*(#|$)' "${EXCLUDE_LIST}" | paste -sd '|' -)"
  if [ -z "${re}" ]; then
    cat
    return 0
  fi
  grep -Ev "${re}" || [ "$?" -eq 1 ]
}

# check_file FILE: FILE against the rule; returns 1 on a violation, having logged it.
function check_file() {
  local -r f="${1}"
  if [[ "${f}" == *_test.go ]]; then
    check_test_file "${f}"
    return
  fi
  case "$(basename "${f}")" in
    main.go | doc.go) return 0 ;;
  esac
  if [ ! -f "${f%.go}_test.go" ]; then
    log "❌ ${f}: no ${f%.go}_test.go"
    return 1
  fi
}

function check_test_file() {
  local -r f="${1}"
  local base="${f%_test.go}"
  [ -f "${base}.go" ] && return 0
  # A tag-named test file: drop trailing _word parts until a source turns up.
  if grep -q '^//go:build ' "${f}"; then
    while [[ "$(basename "${base}")" == *_* ]]; do
      base="${base%_*}"
      [ -f "${base}.go" ] && return 0
    done
  fi
  log "❌ ${f}: no ${f%_test.go}.go; its tests belong in the test file of the source they test"
  return 1
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/lint_test_layout.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
