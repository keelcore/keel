#!/usr/bin/env bash
# scripts/lib/local-hook.sh
# The project hook of a canonical script: the same path under scripts/, with .sh replaced by .local.sh, in
# the repository the script runs in (its git toplevel), so a consumer's hook runs even when the script is run
# from .standards/. Outside a repository (an image build) the hook is the one beside the script. Every
# canonical script calls run_local_hook first, after checking its arguments (audit Rule 10). Source this file.

# run_local_hook SCRIPT [ARG...]: runs SCRIPT's project hook with ARG... when it exists; nothing otherwise. A
# failing hook fails the caller with the hook's status.
function run_local_hook() {
  local -r script="${1}"
  shift
  local hook="${script%.sh}.local.sh" path="${script}" root
  [[ "${path}" == /* ]] || path="${PWD}/${path}"
  root="$(git rev-parse --show-toplevel 2> /dev/null || true)"
  if [ -n "${root}" ] && [[ "${path}" == */scripts/* ]]; then
    hook="${root}/scripts/${path##*/scripts/}"
    hook="${hook%.sh}.local.sh"
  fi
  [ -f "${hook}" ] || return 0
  bash "${hook}" "$@"
}
