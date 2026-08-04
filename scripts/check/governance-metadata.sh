#!/usr/bin/env bash
# scripts/check/governance-metadata.sh
# Validates that every governance standard file (governance/*.md) contains
# all required maturity metadata fields with non-empty values.
#
# Required fields: Maturity, Version, Last Reviewed
# Valid Maturity values: Draft, Recommended, Required, Deprecated

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly REQUIRED_FIELDS=(
  'Maturity'
  'Version'
  'Last Reviewed'
)

readonly VALID_MATURITIES='Draft|Recommended|Required|Deprecated'

function main() {
  exec 5>&1
  validate_env
  check_all_standards
}

function validate_env() {
  if [ ! -d 'governance' ]; then
    log 'ℹ️  governance/ not present in this repo; skipping governance metadata check'
    exit 0
  fi
}

function check_all_standards() {
  log 'Checking governance standards metadata...'
  local failures=0
  local checked=0
  local file
  for file in governance/*.md; do
    [ -f "${file}" ] || continue
    check_standard "${file}" || failures=$((failures + 1))
    checked=$((checked + 1))
  done
  if [ "${checked}" -eq 0 ]; then
    log '⚠️  No governance files found in governance/'
    exit 1
  fi
  if [ "${failures}" -gt 0 ]; then
    log "❌ ${failures} governance file(s) failed metadata validation"
    exit 1
  fi
  log "✅ All ${checked} governance file(s) passed metadata validation"
}

function check_standard() {
  local -r file="${1}"
  local failed=0
  local field
  for field in "${REQUIRED_FIELDS[@]}"; do
    check_field "${file}" "${field}" || failed=1
  done
  check_maturity_value "${file}" || failed=1
  return "${failed}"
}

function check_field() {
  local -r file="${1}"
  local -r field="${2}"
  # Match field label anywhere on a line; permits multi-field-per-line layouts.
  if ! grep -qE "\*\*${field}:\*\* *[^ ]" "${file}"; then
    log "  ❌ ${file}: missing or empty field '**${field}:**'"
    return 1
  fi
}

function check_maturity_value() {
  local -r file="${1}"
  local maturity_line
  maturity_line="$(grep -E '\*\*Maturity:\*\*' "${file}" || true)"
  if [ -z "${maturity_line}" ]; then
    return 1
  fi
  if ! echo "${maturity_line}" | grep -qE "(${VALID_MATURITIES})"; then
    log "  ❌ ${file}: **Maturity:** must be one of: ${VALID_MATURITIES}"
    log "     Got: ${maturity_line}"
    return 1
  fi
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/governance-metadata.log' >&5
}

main "${@:-}"
