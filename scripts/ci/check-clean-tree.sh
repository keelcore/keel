#!/usr/bin/env bash
# scripts/ci/check-clean-tree.sh
# The last step of every CI job that runs tool targets: the working tree must be exactly the
# checked-out commit (`git status --porcelain` empty). Tool targets run against a read-only
# repository locally; in CI the workspace is writable, so a target that writes into the tree (an
# output outside the ignored output directories, a reformatted file, a regenerated artifact)
# fails here as it would locally.
#
# Usage: check-clean-tree.sh   (make check-clean-tree)

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
  local changes
  changes="$(git status --porcelain --untracked-files=all)"
  if [ -n "${changes}" ]; then
    log '❌ the job changed the working tree (tool targets must not write into it):'
    log "${changes}"
    git diff --stat >&5 || true
    exit 1
  fi
  log '✅ working tree clean'
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keelcore_check_clean_tree.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
