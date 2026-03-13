#!/usr/bin/env bash
# newlines.sh
# Check that every tracked .md, .sh, and .go file ends with a trailing newline.
# Exits 1 if violations are found (CI mode).
# Pass --fix to repair violations in place instead of failing.
#
# Run locally:  bash scripts/lint/newlines.sh
# Auto-fix:     bash scripts/lint/newlines.sh --fix

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
  check_newlines "${fix}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_lint_newlines.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] && [ -n "${1:-}" ]; then
    log '❌ Usage: newlines.sh [--fix]'
    exit 1
  fi
  if [ "${#}" -eq 1 ] && [ -n "${1:-}" ] && [ "${1}" != "--fix" ]; then
    log "❌ Unknown argument: ${1}"
    log '❌ Usage: newlines.sh [--fix]'
    exit 1
  fi
}

function tracked_files() {
  git ls-files '*.md' '*.sh' '*.go' | filter_src
}

function missing_newline() {
  local -r f="${1}"
  # Empty files are exempt; non-empty files must end with 0x0a.
  [ -s "${f}" ] || return 1
  [ "$(tail -c1 "${f}" | od -An -tx1 | tr -d ' \n')" != "0a" ]
}

function check_newlines() {
  local -r fix="${1}"
  local failed=0

  while IFS= read -r f; do
    if missing_newline "${f}"; then
      if [ "${fix}" -eq 1 ]; then
        printf '\n' >> "${f}"
        log "  fixed: ${f}"
      else
        log "❌ Missing trailing newline: ${f}"
        failed=1
      fi
    fi
  done < <(tracked_files)

  if [ "${failed}" -ne 0 ]; then
    log 'Run: bash scripts/lint/newlines.sh --fix, then re-stage.'
    exit 1
  fi

  if [ "${fix}" -eq 1 ]; then
    log '✅ Newlines fixed'
  else
    log '✅ Newlines OK'
  fi
}

main "${@:-}"
