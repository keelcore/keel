#!/usr/bin/env bash
# utc.sh
# Repositories are UTC internally (governance/devsecops.md, Reproducibility and Time):
# every timestamp a script, test, workflow or binary writes is UTC. Fails on:
#   - shell `date` calls without -u in tracked *.sh files and Makefiles;
#   - Go time.Local, .Local(), or time.Now().Format without .UTC() in tracked non-test
#     *.go files.
# vendor/, node_modules/ and the .standards submodule are not scanned.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# A `date` invocation: $(date, `date, or date at the start of a command.
readonly BACKTICK=$'\x60'
readonly SHELL_DATE_RE="(\\$\\(|${BACKTICK}|^[[:space:]]*|[;&|][[:space:]]*)date([[:space:]${BACKTICK})]|\$)"
readonly GO_LOCAL_RE='time\.Local\b|\.Local\(\)|time\.Now\(\)\.Format\('

function main() {
  exec 5>&1
  validate_args "${@:-}"
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

function tracked() {
  git ls-files -- "$@" ':!:vendor/**' ':!:**/node_modules/**' ':!:.standards'
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
