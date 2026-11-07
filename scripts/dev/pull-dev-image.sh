#!/usr/bin/env bash
# pull-dev-image.sh
# Fetch the pinned dev image (containers/dev/image.ref) onto this machine, with the runtime's own
# progress. A git commit uses it only from here: it never fetches.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/dev-image.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../lib/dev-image.sh"

readonly IMAGE_REF='containers/dev/image.ref'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local ref
  ref="$(head -1 "${IMAGE_REF}")"
  log "📥 Pulling ${ref}..."
  pull_image "${ref}"
  log "✅ ${ref} is on this machine"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keelcore_dev_pull.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
