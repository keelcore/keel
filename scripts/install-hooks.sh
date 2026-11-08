#!/usr/bin/env bash
# scripts/install-hooks.sh
# `make install-hooks`: point git at the versioned hooks (core.hooksPath scripts/hooks). The path
# resolves from the root of whichever working tree commits, so the same setting serves a clone,
# every worktree and a submodule checkout. Idempotent.
# Project hook: install-hooks.local.sh runs first, when present, with this script's arguments
# (scripts/lib/local-hook.sh: the same path, in the repository this runs in).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=./lib/local-hook.sh
source "$(dirname "${BASH_SOURCE[0]}")/./lib/local-hook.sh"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  log '🪝 Installing git pre-commit hook...'
  install_precommit
}

function install_precommit() {
  if [ ! -f scripts/run-pre-commit.sh ]; then
    log 'ℹ️  scripts/run-pre-commit.sh not present; nothing to install'
    return 0
  fi
  git config core.hooksPath scripts/hooks
  log '✅ hooks installed: core.hooksPath = scripts/hooks (scripts/hooks/pre-commit runs scripts/run-pre-commit.sh)'
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/install-hooks.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
