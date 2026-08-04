#!/usr/bin/env bash
# governance-gate.sh
# CI gate. Wraps `governance-refresh.sh --dry-run` and translates pending
# changes into a non-zero exit so PR CI fails. Contributor must run
# `make governance-refresh`, stage the diff, and push.
#
# Canonical (shipped to consumers via bootstrap-standards.sh). Invoked from
# CI workflow as `make ci-governance-gate`.
#
# Usage: bash governance-gate.sh
#
# Env overrides (testing only — production reads paths from git):
#   GOVREFRESH_STANDARDS_ROOT  passed through to governance-refresh.sh
#   GOVREFRESH_REPO_ROOT       passed through to governance-refresh.sh

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# Locate governance-refresh.sh. Two contexts to handle:
#   (1) Running in the standards repo itself — refresh is a sibling at
#       `$(dirname BASH_SOURCE)/../governance-refresh.sh`.
#   (2) Running in a consumer repo where this gate was copied via bootstrap —
#       refresh lives at `${REPO_ROOT}/.standards/scripts/governance-refresh.sh`
#       (it is NOT shipped to consumers; it stays in the .standards submodule).
# Probe both candidates; use the first that exists.
declare REFRESH_SCRIPT
_sibling="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." 2>/dev/null && pwd)/governance-refresh.sh"
if [ -f "${_sibling}" ]; then
  REFRESH_SCRIPT="${_sibling}"
else
  _repo_root="${GOVREFRESH_REPO_ROOT:-$(git rev-parse --show-toplevel)}"
  REFRESH_SCRIPT="${_repo_root}/.standards/scripts/governance-refresh.sh"
fi
unset _sibling _repo_root
declare -r REFRESH_SCRIPT

function main() {
  exec 5>&1
  validate_args "${@:-}"
  validate_env
  local output exit_code=0
  output="$(bash "${REFRESH_SCRIPT}" --dry-run 2>&1)" || exit_code=$?
  if [ "${exit_code}" -eq 0 ]; then
    log "✅ governance-gate: consumer is in sync with .standards"
    exit 0
  fi
  log "❌ governance-gate: governance drift detected — PR cannot merge."
  log ""
  printf '%s\n' "${output}" | while IFS= read -r line; do log "   ${line}"; done
  log ""
  log "Remediation: run 'make governance-refresh' locally, stage the diff,"
  log "commit, and push."
  exit 1
}

function validate_env() {
  if [ ! -x "${REFRESH_SCRIPT}" ] && [ ! -f "${REFRESH_SCRIPT}" ]; then
    log "❌ governance-gate: missing ${REFRESH_SCRIPT}"
    exit 2
  fi
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log "❌ Error: unexpected argument"
    exit 2
  fi
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/governance_gate.log' >&5
}

main "${@:-}"
