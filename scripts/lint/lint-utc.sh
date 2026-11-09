#!/usr/bin/env bash
# lint-utc.sh
# Repositories are UTC internally (governance/devsecops.md, Reproducibility and Time):
# every timestamp a script, test, workflow or binary writes is UTC. Fails on:
#   - shell `date` calls without -u in tracked *.sh files and Makefiles;
#   - Go time.Local, .Local(), or time.Now().Format without .UTC() in tracked non-test
#     *.go files.
# vendor/ (including nested trees listed in .nolint), node_modules/ and submodules are
# not scanned.
# Project hook: lint-utc.local.sh runs first, when present, with this script's arguments
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

# A `date` invocation: $(date, `date, or date at the start of a command.
readonly BACKTICK=$'\x60'
readonly SHELL_DATE_RE="(\\$\\(|${BACKTICK}|^[[:space:]]*|[;&|][[:space:]]*)date([[:space:]${BACKTICK})]|\$)"
readonly GO_LOCAL_RE='time\.Local\b|\.Local\(\)|time\.Now\(\)\.Format\('

# shellcheck source=../lib/paths.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  local findings
  findings="$(shell_findings; go_findings)"
  if [ -n "${findings}" ]; then
    log '❌ Non-UTC timestamps (use date -u in shell, .UTC() in Go):'
    log "${findings}"
    exit 1
  fi
  log '✅ UTC check passed'
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/lint_utc.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

# tracked: repo-owned files matching the pathspecs. source_files (scripts/lib/paths.sh)
# drops vendor/, submodules and nested vendored trees listed in .nolint.
function tracked() {
  source_files "$@" | grep_exclude -vE '(^|/)node_modules/'
}

# shell_findings prints file:line for date calls that lack -u (comments skipped).
function shell_findings() {
  tracked '*.sh' 'Makefile' '*.mk' 'templates/Makefile*' \
    | xargs grep -HnE "${SHELL_DATE_RE}" 2>/dev/null \
    | grep -vE '^[^:]+:[0-9]+:[[:space:]]*#' \
    | grep -vE 'date[[:space:]]+(-[a-zA-Z]*u|--utc)' || true
}

function go_findings() {
  tracked '*.go' ':!:*_test.go' \
    | xargs grep -HnE "${GO_LOCAL_RE}" 2>/dev/null || true
}

main "${@:-}"
