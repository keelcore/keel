#!/usr/bin/env bash
# scripts/lint/lint-helm.sh
# Helm lint on every repo-owned chart. Arguments: config/helm-args.local.txt.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/paths.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"
# shellcheck source=../lib/tool-config.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/tool-config.sh"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local -a args=()
  local line chart found=0
  while IFS= read -r line; do args+=("${line}"); done < <(helm_args)
  while IFS= read -r chart; do
    [ -n "${chart}" ] || continue
    found=1
    require_tool helm
    log "🔍 helm lint ${chart}"
    helm lint "${chart}" ${args[@]+"${args[@]}"}
  done < <(chart_dirs)
  if [ "${found}" -eq 0 ]; then
    log 'ℹ️  No chart; nothing to lint'
    return 0
  fi
  log '✅ Helm lint passed'
}

# chart_dirs: the directory of each repo-owned chart.
function chart_dirs() {
  local chart
  while IFS= read -r chart; do
    [ -z "${chart}" ] || dirname "${chart}"
  done < <(source_files '*Chart.yaml')
}

# helm_args: config/helm-args.local.txt, one argument per line.
function helm_args() {
  config_replace helm-args
}

function require_tool() {
  command -v "${1}" > /dev/null 2>&1 && return 0
  log "❌ ${1} not found; the tool image carries it (make setup-${1})"
  exit 1
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/lint_helm.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
