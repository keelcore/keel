#!/usr/bin/env bash
# scripts/lint/lint-helm-policy.sh
# kubescape on each render of every repo-owned chart; a failed control outside the allowed list fails.
# Renders: config/helm-policy.local.txt, one per line, tab-separated: NAME, helm arguments, allowed
# control IDs. Without it: one default render, no control allowed.

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
  local chart found=0 failed=0
  while IFS= read -r chart; do
    [ -n "${chart}" ] || continue
    found=1
    require_tool helm
    require_tool kubescape
    check_chart "${chart}" || failed=1
  done < <(chart_dirs)
  if [ "${found}" -eq 0 ]; then
    log 'ℹ️  No chart; nothing to scan'
    return 0
  fi
  [ "${failed}" -eq 0 ] || exit 1
  log '✅ Chart policy scan clean'
}

# renders: the configured renders, or the one default render.
function renders() {
  local configured
  configured="$(config_replace helm-policy)"
  if [ -n "${configured}" ]; then printf '%s\n' "${configured}"; else printf 'default\t\t\n'; fi
}

function check_chart() {
  local -r chart="${1}"
  local work name args allowed failed=0
  local -a words
  work="$(mktemp -d)"
  while IFS=$'\t' read -r name args allowed; do
    read -r -a words <<< "${args}"
    helm template policy "${chart}" ${words[@]+"${words[@]}"} > "${work}/${name}.yaml"
    check_render "${work}" "${name}" "${allowed}" || failed=1
  done < <(renders)
  rm -rf "${work}"
  return "${failed}"
}

function check_render() {
  local -r work="${1}" name="${2}" allowed="${3}"
  log "🔍 kubescape: ${name}"
  kubescape scan "${work}/${name}.yaml" --format json --output "${work}/${name}.json" > /dev/null
  local unexpected
  unexpected="$(failed_controls "${work}/${name}.json" | grep -vxF -f <(tr ' ' '\n' <<< "${allowed}") || true)"
  [ -z "${unexpected}" ] && return 0
  log "❌ ${name} fails controls:"
  log "${unexpected}"
  return 1
}

# failed_controls prints the ID of each failed control.
function failed_controls() {
  jq -r '.summaryDetails.controls | to_entries[] | select(.value.status == "failed") | .key' "${1}"
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
  printf '%s\n' "${msg}" | tee -a '/tmp/lint_helm_policy.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
