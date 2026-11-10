#!/usr/bin/env bash
# scripts/lint-all.local.sh
# keel's part of the canonical scripts/lint-all.sh, which runs it first: the commit hook and
# `make lint-all` run keel's own linters. Today: the unit-test layout
# (scripts/lint/lint-test-layout.local.sh, #40). `make lint` runs it as the lint-test-layout target.
# Project-owned, never canonical.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

function main() {
  exec 5>&1
  validate_args "${@:-}"
  bash "$(dirname "${BASH_SOURCE[0]}")/lint/lint-test-layout.local.sh"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/lint_all_local.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
