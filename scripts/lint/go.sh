#!/usr/bin/env bash
# go.sh
# Check that every repo-owned Go source file is gofmt-clean.
#
# Scoped to superproject-tracked *.go via go_source_files (lib/paths.sh), so
# vendored and submodule trees (vendor/, vendors/, .standards/) are excluded —
# their formatting is the upstream's concern, not ours. This is the gate that was
# missing: Go edits (including test files added by a coverage ratchet) could reach
# a commit with no gofmt check in the lint set.
#
# Exits 1 if any file is not gofmt-clean (CI mode).
# Pass --fix to rewrite offending files in place instead of failing.
#
# Run locally:  bash scripts/lint/go.sh
# Auto-fix:     bash scripts/lint/go.sh --fix

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
  local fix=0
  [ "${1:-}" = "--fix" ] && fix=1
  check_gofmt "${fix}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_lint_go.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] && [ -n "${1:-}" ]; then
    log '❌ Usage: go.sh [--fix]'
    exit 1
  fi
  if [ "${#}" -eq 1 ] && [ -n "${1:-}" ] && [ "${1}" != "--fix" ]; then
    log "❌ Unknown argument: ${1}"
    log '❌ Usage: go.sh [--fix]'
    exit 1
  fi
}

function ensure_gofmt() {
  command -v gofmt >/dev/null 2>&1 && return 0
  log '❌ gofmt not found on PATH (install the Go toolchain)'
  exit 1
}

function check_gofmt() {
  local -r fix="${1}"
  ensure_gofmt
  local files
  files="$(go_source_files)"
  if [ -z "${files}" ]; then
    log '✅ gofmt: no Go source files to check'
    return 0
  fi
  if [ "${fix}" -eq 1 ]; then
    printf '%s' "${files}" | tr '\n' '\0' | xargs -0 gofmt -w
    log '✅ gofmt: Go sources formatted'
    return 0
  fi
  local unformatted
  unformatted="$(printf '%s' "${files}" | tr '\n' '\0' | xargs -0 gofmt -l)"
  if [ -n "${unformatted}" ]; then
    log '❌ gofmt: these Go files are not formatted:'
    printf '%s\n' "${unformatted}" | while IFS= read -r offender; do log "   ${offender}"; done
    log '   Fix with: bash scripts/lint/go.sh --fix'
    exit 1
  fi
  log '✅ gofmt: all Go sources formatted'
}

main "${@:-}"
