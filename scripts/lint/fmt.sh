#!/usr/bin/env bash
# fmt.sh
# Run gofmt -w -s on all non-vendor, non-submodule Go source files.

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
  log 'Running gofmt -w -s'
  run_fmt
  log '✅ Format complete'
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_lint_fmt.log' >&5
}

function validate_args() { :; }

function run_fmt() {
  local files
  files="$(go_source_files)"
  if [ -z "${files}" ]; then
    log 'No Go files to format'
    return 0
  fi
  while IFS= read -r f; do
    gofmt -w -s "${f}"
  done <<< "${files}"
}

main "${@:-}"
