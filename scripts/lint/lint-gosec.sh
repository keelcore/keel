#!/usr/bin/env bash
# scripts/lint/lint-gosec.sh
# Go SAST: gosec in every repo-owned Go module, never .standards/ or another submodule, which scan their
# own code. Excluded rules: config/gosec.local.txt. Findings reviewed as false positives are accepted one site
# at a time in config/gosec-sites.local.txt (RULE<TAB>PATH<TAB>CODE<TAB>REASON, CODE the flagged line, trimmed):
# every other finding fails, and so does a listed site gosec no longer reports.
# Project hook: lint-gosec.local.sh runs first, when present, with this script's arguments
# (scripts/lib/local-hook.sh: the same path, in the repository this runs in).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/local-hook.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/local-hook.sh"

# shellcheck source=../lib/paths.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"
# shellcheck source=../lib/tool-config.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/tool-config.sh"

readonly SITES='config/gosec-sites.local.txt'

WORK_DIR=''

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  local root
  root="$(cd "$(git rev-parse --show-toplevel)" && pwd -P)"
  if [ -z "$(source_files '*go.mod')" ]; then
    log 'ℹ️  No Go module; nothing to check'
    return 0
  fi
  trap remove_work_dir EXIT
  WORK_DIR="$(mktemp -d)"
  scan "${root}" > "${WORK_DIR}/found"
  site_keys | LC_ALL=C sort -u > "${WORK_DIR}/listed"
  local failed=0 rule path line code
  while IFS=$'\t' read -r rule path line code; do
    if ! grep -qxF -- "$(printf '%s\t%s\t%s' "${rule}" "${path}" "${code}")" "${WORK_DIR}/listed"; then
      log "❌ ${rule} ${path}:${line}: ${code}"
      failed=1
    fi
  done < "${WORK_DIR}/found"
  cut -f1,2,4 "${WORK_DIR}/found" | LC_ALL=C sort -u > "${WORK_DIR}/reported"
  while IFS=$'\t' read -r rule path code; do
    log "❌ stale site in ${SITES} (gosec no longer reports it): ${rule} ${path}: ${code}"
    failed=1
  done < <(LC_ALL=C comm -23 "${WORK_DIR}/listed" "${WORK_DIR}/reported")
  [ "${failed}" -eq 0 ] || exit 1
  log "✅ gosec clean ($(wc -l < "${WORK_DIR}/listed" | tr -d ' ') reviewed sites in ${SITES})"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/lint_gosec.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

function remove_work_dir() {
  [ -z "${WORK_DIR}" ] || rm -rf "${WORK_DIR}"
}

# scan ROOT: "RULE<TAB>PATH<TAB>LINE<TAB>CODE" for every finding in the repository's own modules, PATH from ROOT.
function scan() {
  local -r root="${1}"
  local -a args=(-quiet -fmt=json -no-fail -exclude-dir=vendor -exclude-dir=.standards)
  local rules mod sm
  rules="$(config_list gosec | paste -sd, -)"
  [ -z "${rules}" ] || args+=("-exclude=${rules}")
  while IFS= read -r sm; do
    [ -z "${sm}" ] || args+=("-exclude-dir=${sm}")
  done < <(submodule_paths)
  while IFS= read -r mod; do
    [ -n "${mod}" ] || continue
    log "🔍 gosec in $(dirname "${mod}") (skipping submodules${rules:+; excluding ${rules}})"
    (cd "$(dirname "${mod}")" && gosec "${args[@]}" ./...) \
      | jq -r '.Issues[]? | [.rule_id, .file, (.line | tostring | split("-")[0])] | @tsv' \
      | while IFS=$'\t' read -r rule file line; do
        file="$(cd "$(dirname "${file}")" && pwd -P)/$(basename "${file}")"
        printf '%s\t%s\t%s\t%s\n' "${rule}" "${file#"${root}/"}" "${line}" \
          "$(sed -n "${line}p" "${file}" | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//')"
      done
  done < <(source_files '*go.mod')
}

# site_keys: "RULE<TAB>PATH<TAB>CODE" of every reviewed site.
function site_keys() {
  [ -f "${SITES}" ] || return 0
  grep -vE '^[[:space:]]*(#|$)' "${SITES}" | cut -f1-3
}

main "${@:-}"
