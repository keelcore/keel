#!/usr/bin/env bash
# update-dev-image-ref.sh
# Write containers/NAME/image.ref: the name of the image built from what the next commit holds,
# <repository>:ctx-<hash>. The hash comes from the staged files, needing no network and no image, so
# a commit always names its own image. Run by the pre-commit hook, which stages the result into the
# commit being made; `make update-dev-image-ref` runs it by hand.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# The commit's own index: a partial commit stages into a temporary one, which the library clears.
readonly COMMIT_INDEX="${GIT_INDEX_FILE:-}"

# shellcheck source=../lib/dev-image.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../lib/dev-image.sh"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  [ -z "${COMMIT_INDEX}" ] || export GIT_INDEX_FILE="${COMMIT_INDEX}"
  export DEV_CONTEXT_STAGED=1
  local cf name file ref
  for cf in containers/*/Containerfile; do
    git cat-file -e ":${cf}" 2> /dev/null || continue
    name="$(basename "$(dirname "${cf}")")"
    file="$(dev_ref_file "${name}")"
    ref="$(dev_pin_ref . "${name}")"
    if [ "$(git show ":${file}" 2> /dev/null || true)" != "${ref}" ]; then
      printf '%s\n' "${ref}" > "${file}"
      git add -- "${file}"
      log "📌 ${file}: ${ref}"
    fi
  done
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
