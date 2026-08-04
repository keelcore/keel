#!/usr/bin/env bash
# scripts/lint/markdown.sh
# Lints all GIT-TRACKED Markdown files using markdownlint-cli2.
# Tracked-files scope means gitignored content (.scratch/, build outputs,
# vendored caches) is automatically excluded — it isn't part of the repo.
# Requires node and markdownlint-cli2 on PATH (npm install or npx).
# `bash scripts/X.sh` does not source interactive shell init files, so
# Homebrew/nvm/fnm/volta PATH additions are absent — the node-path helper
# probes the common install locations and prepends the right dir.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=lib/node-path.sh
. "$(dirname "${BASH_SOURCE[0]}")/../lib/node-path.sh"
# shellcheck source=lib/paths.sh
. "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"

function main() {
  exec 5>&1
  validate_env
  lint
}

function validate_env() {
  log 'Checking for node...'
  if ! node_path_ensure; then
    log '❌ node not found on PATH and not discoverable at standard install locations.'
    log '   markdownlint-cli2 is a node-based tool; install node (e.g. `brew install node`)'
    log '   or extend scripts/lib/node-path.sh with your install path, then retry.'
    exit 1
  fi
  log "✅ node available ($(command -v node))"

  log 'Checking for markdownlint-cli2...'
  if ! command -v markdownlint-cli2 > /dev/null 2>&1; then
    if command -v npx > /dev/null 2>&1; then
      log 'markdownlint-cli2 not on PATH; will use npx'
    else
      log '❌ Neither markdownlint-cli2 nor npx found; run: npm install'
      exit 1
    fi
  fi
  log '✅ Tool available'
}

function lint() {
  log 'Linting Markdown files...'
  local rc
  rc=0
  run_linter || rc="${?}"
  if [ "${rc}" -ne 0 ]; then
    log "❌ Markdown lint failed (exit ${rc})"
    exit "${rc}"
  fi
  log '✅ All Markdown files passed'
}

function run_linter() {
  local -a files=()
  local f
  while IFS= read -r f; do
    files+=("${f}")
  done < <(git ls-files '*.md' | nolint_filter)
  if [ "${#files[@]}" -eq 0 ]; then
    log 'ℹ️  No tracked .md files to lint'
    return 0
  fi
  if command -v markdownlint-cli2 > /dev/null 2>&1; then
    markdownlint-cli2 "${files[@]}"
  else
    npx --yes markdownlint-cli2 "${files[@]}"
  fi
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/markdown-lint.log' >&5
}

main "${@:-}"
