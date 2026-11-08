#!/usr/bin/env bash
# scripts/lint-all.sh
# Top-level lint entry point. Composes per-domain linters in scripts/lint/
# and the per-artifact metadata checks in scripts/check/.
#
# Checks that depend on repository-specific artifacts (ADRs, RFCs, governance
# standards) are skipped when the corresponding directory is absent, so the
# same canonical script runs unchanged in consumer repos that do not carry
# those artifacts.
# Project hook: lint-all.local.sh runs first, when present, with this script's arguments
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
  log 'Running all linters...'
  run_markdown_lint
  run_shellcheck
  run_newlines_lint
  run_go_lint
  run_forbid_suppressions
  run_adr_check
  run_governance_check
  run_rfc_check
  log '✅ All linters passed'
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/lint.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

function run_markdown_lint() {
  log 'Running Markdown lint...'
  bash scripts/lint/lint-markdown.sh
}

function run_shellcheck() {
  log 'Running shellcheck...'
  bash scripts/lint/lint-bash.sh
}

function run_newlines_lint() {
  log 'Running trailing-newline check...'
  bash scripts/lint/lint-newlines.sh
}

function run_go_lint() {
  log 'Running gofmt check...'
  bash scripts/lint/lint-go.sh
}

function run_forbid_suppressions() {
  # Enforces feedback_never_bypass_lint: no shellcheck-disable / markdownlint-disable
  # directives in tracked files. Skipped if the consumer doesn't carry the check.
  if [ ! -x 'scripts/lint/lint-suppressions.sh' ]; then
    log 'Skipping suppression-directive check (lint-suppressions.sh not present)'
    return 0
  fi
  log 'Checking for lint-suppression directives...'
  bash scripts/lint/lint-suppressions.sh
}

function run_adr_check() {
  if [ ! -d 'docs/adr' ]; then
    log 'Skipping ADR metadata check (no docs/adr/ in this repo)'
    return 0
  fi
  log 'Checking ADR metadata...'
  bash scripts/check/check-adr-metadata.sh
}

function run_governance_check() {
  if [ ! -d 'governance' ]; then
    log 'Skipping governance metadata check (no governance/ in this repo)'
    return 0
  fi
  log 'Checking governance metadata...'
  bash scripts/check/check-governance-metadata.sh
}

function run_rfc_check() {
  if [ ! -d 'docs/rfc' ]; then
    log 'Skipping RFC metadata check (no docs/rfc/ in this repo)'
    return 0
  fi
  log 'Checking RFC metadata...'
  bash scripts/check/check-rfc-metadata.sh
}

main "${@:-}"
