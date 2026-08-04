#!/usr/bin/env bash
# scripts/check/adr-metadata.sh
# Validates that every ADR file (docs/adr/[0-9]*.md) contains all required metadata fields
# with non-empty values. Skips the template (0000-template.md).
#
# Required fields: Date, Status, Driver, Approver, Contributors, Informed,
#                  Supersedes, Superseded By

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly REQUIRED_FIELDS=(
  'Date'
  'Status'
  'Driver'
  'Approver'
  'Contributors'
  'Informed'
  'Supersedes'
  'Superseded By'
)

readonly VALID_STATUSES='Proposed|Accepted|Rejected|Superseded|Deprecated'

function main() {
  exec 5>&1
  validate_env
  check_all_adrs
}

function validate_env() {
  if [ ! -d 'docs/adr' ]; then
    log 'ℹ️  docs/adr/ not present in this repo; skipping ADR metadata check'
    exit 0
  fi
}

function check_all_adrs() {
  log 'Checking ADR metadata...'
  local failures=0
  local checked=0
  local file
  for file in docs/adr/[0-9][0-9][0-9][0-9]-*.md; do
    [ -f "${file}" ] || continue
    # Skip the template file
    case "${file}" in
      *0000-template.md) continue ;;
    esac
    check_adr "${file}" || failures=$((failures + 1))
    checked=$((checked + 1))
  done
  if [ "${checked}" -eq 0 ]; then
    log '⚠️  No ADR files found (excluding template)'
    exit 1
  fi
  if [ "${failures}" -gt 0 ]; then
    log "❌ ${failures} ADR(s) failed metadata validation"
    exit 1
  fi
  log "✅ All ${checked} ADR(s) passed metadata validation"
}

function check_adr() {
  local -r file="${1}"
  local failed=0
  local field
  for field in "${REQUIRED_FIELDS[@]}"; do
    check_field "${file}" "${field}" || failed=1
  done
  check_status_value "${file}" || failed=1
  return "${failed}"
}

function check_field() {
  local -r file="${1}"
  local -r field="${2}"
  # Match the field label anywhere on a line (not just at line-start) followed
  # by at least one non-space character. Permits multi-field-per-line layouts
  # (e.g. "**Date:** 2026-05-01 **Status:** Proposed") which prettier and other
  # formatters commonly produce.
  if ! grep -qE "\*\*${field}:\*\* *[^ ]" "${file}"; then
    log "  ❌ ${file}: missing or empty field '**${field}:**'"
    return 1
  fi
}

function check_status_value() {
  local -r file="${1}"
  local status_line
  status_line="$(grep -E '\*\*Status:\*\*' "${file}" || true)"
  if [ -z "${status_line}" ]; then
    return 1
  fi
  if ! echo "${status_line}" | grep -qE "(${VALID_STATUSES})"; then
    log "  ❌ ${file}: **Status:** must be one of: ${VALID_STATUSES}"
    log "     Got: ${status_line}"
    return 1
  fi
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/adr-metadata.log' >&5
}

main "${@:-}"
