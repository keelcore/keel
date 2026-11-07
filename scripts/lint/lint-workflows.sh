#!/usr/bin/env bash
# scripts/lint/lint-workflows.sh
# Workflow security lint: actionlint (syntax, expressions, shellcheck of run: blocks) and
# zizmor (template injection, credential persistence, cache poisoning, pin/version drift).
# zizmor runs online audits when GH_TOKEN is set, offline otherwise.
# Install with: make setup-actionlint setup-zizmor

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly WORKFLOWS='.github/workflows'
readonly ZIZMOR_CONFIG='config/zizmor.local.yml'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  if [ -z "$(find "${WORKFLOWS}" -name '*.yml' 2> /dev/null)" ]; then
    log 'ℹ️  No workflows; nothing to lint'
    return 0
  fi
  require_tool actionlint
  require_tool zizmor
  log '🔍 actionlint'
  actionlint
  log '🔍 zizmor'
  run_zizmor
  log '✅ Workflow lint clean'
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/lint_workflows.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

function run_zizmor() {
  local -a mode=('--offline')
  [ -z "${GH_TOKEN:-}" ] || mode=()
  [ ! -f "${ZIZMOR_CONFIG}" ] || mode+=(--config "${ZIZMOR_CONFIG}")
  zizmor ${mode[@]+"${mode[@]}"} "${WORKFLOWS}"
}

function require_tool() {
  local -r tool="${1}"
  if ! command -v "${tool}" >/dev/null 2>&1; then
    log "ERROR: ${tool} not found in PATH"
    log "  Install via: make setup-${tool}"
    exit 1
  fi
}

main "${@:-}"
