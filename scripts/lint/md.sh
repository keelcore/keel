#!/usr/bin/env bash
# md.sh
# Lint all tracked markdown files using markdownlint-cli.
# Config: .markdownlint.json at repo root.
# Runnable locally and in CI identically.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/paths.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  lint_markdown
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_lint_md.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

function tracked_md_files() {
  git ls-files '*.md' | filter_src | while IFS= read -r f; do
    [ -f "${f}" ] && [ ! -L "${f}" ] && printf '%s\n' "${f}"
  done
}

function lint_markdown() {
  local files
  files="$(tracked_md_files)"
  if [ -z "${files}" ]; then
    log '✅ No markdown files to lint'
    return 0
  fi
  log '🔍 Linting markdown files...'
  # xargs passes files as arguments; markdownlint reads config from .markdownlint.json
  printf '%s\n' "${files}" | xargs markdownlint --config .markdownlint.json
  log '✅ Markdown lint passed'
}

main "${@:-}"
