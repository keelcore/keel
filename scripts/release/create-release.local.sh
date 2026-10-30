#!/usr/bin/env bash
# create-release.local.sh
# keel bump hook for the canonical scripts/release/create-release.sh
# (`make create-release`). Project-owned, never canonical.
#
# Usage: create-release.local.sh <previous-tag>
#
# Diffs the config field set of pkg/config/schema.yaml at <previous-tag>
# against HEAD:
#   removed fields -> major (breaking)
#   added fields   -> minor
#   no change      -> patch
# The driver applies pre-1.0 demotion and notes it in the subject.
#
# stdout is the hook contract: line 1 the bump level, line 2 the tag subject.
# The field lists go to stderr (the driver shows them and records them in the
# tag body).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

declare -r SCHEMA_PATH='pkg/config/schema.yaml'
declare -r LOG_FILE='/tmp/keel_create_release_local.log'

function main() {
  # stdout carries the hook contract; all messaging goes to stderr.
  exec 5>&2
  validate_args "${@:-}"
  local old_fields new_fields
  old_fields="$(schema_fields_at_tag "${1}")"
  new_fields="$(schema_fields_at_head)"
  emit_result "${1}" "$(removed_fields "${old_fields}" "${new_fields}")" "$(added_fields "${old_fields}" "${new_fields}")"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a "${LOG_FILE}" >&5
}

function validate_args() {
  if [ "${#}" -ne 1 ] || [ -z "${1:-}" ]; then
    log 'Usage: create-release.local.sh <previous-tag>'
    exit 1
  fi
}

function extract_fields() {
  # Reads JSON Schema YAML from stdin; prints sorted flat dotted field paths.
  go run ./cmd/config-schema/ --fields
}

function schema_fields_at_tag() {
  local -r tag="${1}"
  local output
  if ! output="$(git show "${tag}:${SCHEMA_PATH}" 2>/dev/null)"; then
    log "WARNING: ${SCHEMA_PATH} did not exist at tag ${tag}; treating all current fields as added"
    printf ''
    return 0
  fi
  printf '%s\n' "${output}" | extract_fields
}

function schema_fields_at_head() {
  extract_fields < "${SCHEMA_PATH}"
}

function sorted_fields() {
  printf '%s\n' "${1}" | grep -v '^[[:space:]]*$' | sort
}

function removed_fields() {
  comm -23 <(sorted_fields "${1}") <(sorted_fields "${2}")
}

function added_fields() {
  comm -13 <(sorted_fields "${1}") <(sorted_fields "${2}")
}

function emit_result() {
  local -r tag="${1}" removed="${2}" added="${3}"
  local bump
  bump="$(detect_bump "${removed}" "${added}")"
  log "Schema: ${SCHEMA_PATH} (since ${tag})"
  print_field_list 'Fields removed (BREAKING):' "${removed}"
  print_field_list 'Fields added:' "${added}"
  printf '%s\n' "${bump}"
  printf '%s\n' "$(build_tag_subject "${bump}" "${removed}" "${added}")"
}

function detect_bump() {
  local -r removed="${1}" added="${2}"
  if [ -n "${removed}" ]; then
    printf 'major'
  elif [ -n "${added}" ]; then
    printf 'minor'
  else
    printf 'patch'
  fi
}

function count_fields() {
  printf '%s\n' "${1}" | grep -c '[^[:space:]]' || true
}

function build_tag_subject() {
  local -r bump="${1}" removed="${2}" added="${3}"
  case "${bump}" in
    major) printf 'removed %s config field(s)' "$(count_fields "${removed}")" ;;
    minor) printf 'added %s config field(s)' "$(count_fields "${added}")" ;;
    patch) printf 'internal improvements; no config surface changes' ;;
  esac
}

function print_field_list() {
  local -r label="${1}" fields="${2}"
  local field
  if [ -z "${fields}" ]; then
    log "${label} none"
    return 0
  fi
  log "${label}"
  while IFS= read -r field; do
    [ -z "${field}" ] && continue
    log "  ${field}"
  done <<< "${fields}"
}

main "${@:-}"
