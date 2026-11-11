#!/usr/bin/env bash
# update-dev-image-ref.sh
# Write each tool image's name (containers/NAME/image.ref; a listed image's <BUILD FILE>.image.ref, beside its
# build file: tool_images in scripts/lib/dev-image.sh): the name of the image built from what the next commit holds,
# <repository>:ctx-<hash>. The hash comes from the staged files, needing no network and no image, so
# a commit always names its own image. Run by the pre-commit hook, which stages the result into the
# commit being made. By hand, name the index: GIT_INDEX_FILE=.git/index make update-dev-image-ref.
# Environment: GIT_INDEX_FILE (default DeadBeef; git sets it in the commit hook: the commit's own index,
# a temporary one for a partial commit). The default is never a real index: the script fails on it.
# Project hook: update-dev-image-ref.local.sh runs first, when present, with this script's arguments
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

declare -rx GIT_INDEX_FILE="${GIT_INDEX_FILE:-DeadBeef}"

# shellcheck source=../lib/dev-image.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../lib/dev-image.sh"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  require_index
  local list name cf file ref
  list="$(tool_images staged)"
  while IFS=$'\t' read -r name cf _ file; do
    [ -n "${name}" ] || continue
    git cat-file -e ":${cf}" 2> /dev/null || continue
    ref="$(dev_pin_ref . "${name}" staged)"
    if [ "$(git show ":${file}" 2> /dev/null || true)" != "${ref}" ]; then
      printf '%s\n' "${ref}" > "${file}"
      git add -- "${file}"
      log "📌 ${file}: ${ref}"
    fi
  done <<< "${list}"
}

# require_index: git sets GIT_INDEX_FILE in the commit hook; a manual run names the index. A missing or
# nonexistent index reads as empty, so the name would be of an empty context: fail instead.
function require_index() {
  [ "${GIT_INDEX_FILE}" != 'DeadBeef' ] && return 0
  log '❌ GIT_INDEX_FILE is not set: the commit hook sets it; by hand, name the index:'
  log '   GIT_INDEX_FILE=.git/index make update-dev-image-ref'
  exit 1
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
