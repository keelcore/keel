#!/usr/bin/env bash
# build-image.local.sh
# Put keel's dev image for this tree on this machine: pulled when its context is published, else
# built locally (scripts/dev/run.sh --fetch-image). Nothing is pushed. Project-owned, never canonical.

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
  bash "$(dirname "${BASH_SOURCE[0]}")/run.sh" --fetch-image
}

function log() {
  printf '%s\n' "${1:-}" >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
