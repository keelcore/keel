#!/usr/bin/env bash
# scripts/fresh-repo.sh
# First-time repo setup: download Go module dependencies so the tree is ready
# to build. Git hooks are installed by the `install-hooks` prerequisite of the
# `fresh-repo` Makefile target, not here.

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
  log '📦 Installing Go tools...'
  go mod download
  log '✅ Repo ready'
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_fresh_repo.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
