#!/usr/bin/env bash
# scripts/install-hooks.sh
# Project-specific wrapper for `make install-hooks`: symlink the pre-commit
# entry into .git/hooks/. Idempotent — re-running refreshes the symlink target.

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
  log '🪝 Installing git pre-commit hook...'
  install_precommit
}

function install_precommit() {
  if [ ! -f scripts/git_precommit.sh ]; then
    log 'ℹ️  scripts/git_precommit.sh not present; nothing to install'
    return 0
  fi
  ln -sf ../../scripts/git_precommit.sh .git/hooks/pre-commit
  log '✅ pre-commit hook installed (.git/hooks/pre-commit → ../../scripts/git_precommit.sh)'
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
