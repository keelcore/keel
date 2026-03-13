#!/usr/bin/env bash
# check-legal-drift.sh
# Verifies that root LICENSE and TRADEMARK.md are identical to pkg/clisupport/ copies.
# In CI (CI=true): fails with a plain error. Locally: suggests the cp command to run.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly PAIRS=(
  'LICENSE:pkg/clisupport/LICENSE'
  'TRADEMARK.md:pkg/clisupport/TRADEMARK.md'
)

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local root
  root="$(git rev-parse --show-toplevel)"
  local failed=0
  for pair in "${PAIRS[@]}"; do
    check_pair "${root}" "${pair}" || failed=1
  done
  [ "${failed}" -eq 0 ]
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_check_legal_drift.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 0 ] && [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected arg'
    exit 1
  fi
}

function check_pair() {
  local -r root="${1}"
  local -r pair="${2}"
  local -r root_file="${root}/${pair%%:*}"
  local -r pkg_file="${root}/${pair##*:}"
  if diff -q "${root_file}" "${pkg_file}" > /dev/null 2>&1; then
    return 0
  fi
  advise_fix "${root_file}" "${pkg_file}"
  return 1
}

function file_commit_time() {
  git log -1 --format='%ct' -- "${1}" 2>/dev/null || printf '0'
}

function advise_fix() {
  local -r root_file="${1}"
  local -r pkg_file="${2}"
  log "❌ Legal file drift detected:"
  log "  ${root_file}"
  log "  ${pkg_file}"
  if [ "${CI:-}" = 'true' ]; then
    log "  Sync the files locally and commit before pushing."
    return
  fi
  local root_ts pkg_ts
  root_ts="$(file_commit_time "${root_file}")"
  pkg_ts="$(file_commit_time "${pkg_file}")"
  if [ "${root_ts}" -gt "${pkg_ts}" ]; then
    log "  Fix: cp ${root_file} ${pkg_file}"
  elif [ "${pkg_ts}" -gt "${root_ts}" ]; then
    log "  Fix: cp ${pkg_file} ${root_file}"
  else
    log "  Both files modified since last common commit — merge manually."
  fi
}

main "${@:-}"
