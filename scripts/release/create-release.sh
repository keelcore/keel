#!/usr/bin/env bash
# create-release.sh
# Compute and tag the next semver release from schema.yaml field-diff.
#
# Usage:
#   create-release.sh                  Auto-compute version from schema diff
#   create-release.sh --force vX.Y.Z  Force a specific version (validated)

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly SCHEMA_PATH='pkg/config/schema.yaml'
readonly LOG_FILE='/tmp/keel_create_release.log'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local force_version
  force_version="$(parse_force_flag "${@:-}")"
  check_preconditions
  run_release "${force_version}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a "${LOG_FILE}" >&5
}

function validate_args() {
  # Accept 0 args (auto) or exactly 2 args (--force vX.Y.Z).
  # Tolerate the single-empty-string GHA artifact by treating it as 0 args.
  local count="${#}"
  if [ "${count}" -eq 1 ] && [ -z "${1:-}" ]; then
    return 0
  fi
  if [ "${count}" -eq 0 ]; then
    return 0
  fi
  if [ "${count}" -eq 2 ]; then
    if [ "${1:-}" != '--force' ]; then
      printf 'Usage: create-release.sh [--force vX.Y.Z]\n' >&2
      exit 1
    fi
    if ! [[ "${2:-}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
      printf 'Error: version must match vX.Y.Z (e.g. v1.2.3)\n' >&2
      exit 1
    fi
    return 0
  fi
  printf 'Usage: create-release.sh [--force vX.Y.Z]\n' >&2
  exit 1
}

function parse_force_flag() {
  # If fewer than 2 real args (or first arg empty), return empty string.
  local count="${#}"
  if [ "${count}" -lt 2 ] || [ -z "${1:-}" ]; then
    printf ''
    return 0
  fi
  strip_v "${2:-}"
}

function ensure_repo_root() {
  local root
  root="$(git rev-parse --show-toplevel)"
  cd "${root}"
}

function check_branch() {
  local branch
  branch="$(git rev-parse --abbrev-ref HEAD)"
  if [ "${branch}" != 'main' ]; then
    log "ERROR: must be on main branch (currently on '${branch}')"
    exit 1
  fi
}

function fetch_and_verify_head() {
  git fetch origin
  local local_sha remote_sha
  local_sha="$(git rev-parse HEAD)"
  remote_sha="$(git rev-parse origin/main)"
  if [ "${local_sha}" != "${remote_sha}" ]; then
    log "ERROR: local HEAD (${local_sha:0:7}) differs from origin/main (${remote_sha:0:7})"
    log "       Run: git pull --ff-only origin main"
    exit 1
  fi
}

function check_clean_tree() {
  if ! git diff --quiet || ! git diff --cached --quiet; then
    log "ERROR: working tree is not clean; commit or stash changes first"
    exit 1
  fi
}

function check_preconditions() {
  ensure_repo_root
  check_branch
  fetch_and_verify_head
  check_clean_tree
}

function current_git_tag() {
  local tag
  if ! tag="$(git describe --tags --abbrev=0 2>/dev/null)"; then
    log "ERROR: no tags found in repository"
    log "       Create an initial tag first: git tag -a v0.1.0 -m 'initial release'"
    exit 1
  fi
  printf '%s' "${tag}"
}

function strip_v() {
  printf '%s' "${1}" | sed 's/^v//'
}

function extract_fields() {
  # Reads JSON Schema YAML from stdin; prints sorted flat dotted field paths.
  go run ./cmd/config-schema/ --fields
}

function schema_fields_at_tag() {
  local tag="${1}"
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

function detect_bump() {
  local removed="${1}"
  local added="${2}"
  if [ -n "${removed}" ]; then
    printf 'major'
  elif [ -n "${added}" ]; then
    printf 'minor'
  else
    printf 'patch'
  fi
}

function semver_major() {
  printf '%s' "${1}" | cut -d. -f1
}

function semver_minor() {
  printf '%s' "${1}" | cut -d. -f2
}

function semver_patch() {
  printf '%s' "${1}" | cut -d. -f3
}

function semver_gt() {
  local a="${1}" b="${2}"
  local a_maj a_min a_pat b_maj b_min b_pat
  IFS='.' read -r a_maj a_min a_pat <<< "${a}"
  IFS='.' read -r b_maj b_min b_pat <<< "${b}"
  if [ "${a_maj}" -gt "${b_maj}" ]; then return 0; fi
  if [ "${a_maj}" -lt "${b_maj}" ]; then return 1; fi
  if [ "${a_min}" -gt "${b_min}" ]; then return 0; fi
  if [ "${a_min}" -lt "${b_min}" ]; then return 1; fi
  if [ "${a_pat}" -gt "${b_pat}" ]; then return 0; fi
  return 1
}

function compute_version() {
  local cur="${1}" bump="${2}"
  local maj min pat
  maj="$(semver_major "${cur}")"
  min="$(semver_minor "${cur}")"
  pat="$(semver_patch "${cur}")"
  case "${bump}" in
    major) printf '%s.0.0' "$((maj + 1))" ;;
    minor) printf '%s.%s.0' "${maj}" "$((min + 1))" ;;
    patch) printf '%s.%s.%s' "${maj}" "${min}" "$((pat + 1))" ;;
  esac
}

function is_single_minor_bump() {
  # Returns 0 if force == cur_maj.(cur_min+1).0 exactly.
  local f_maj f_min f_pat c_maj c_min
  f_maj="$(semver_major "${1}")"; f_min="$(semver_minor "${1}")"; f_pat="$(semver_patch "${1}")"
  c_maj="$(semver_major "${2}")"; c_min="$(semver_minor "${2}")"
  [ "${f_maj}" -eq "${c_maj}" ] && [ "${f_min}" -eq "$((c_min + 1))" ] && [ "${f_pat}" -eq 0 ]
}

function is_single_patch_bump() {
  # Returns 0 if force == cur_maj.cur_min.(cur_pat+1) exactly.
  local f_maj f_min f_pat c_maj c_min c_pat
  f_maj="$(semver_major "${1}")"; f_min="$(semver_minor "${1}")"; f_pat="$(semver_patch "${1}")"
  c_maj="$(semver_major "${2}")"; c_min="$(semver_minor "${2}")"; c_pat="$(semver_patch "${2}")"
  [ "${f_maj}" -eq "${c_maj}" ] && [ "${f_min}" -eq "${c_min}" ] && [ "${f_pat}" -eq "$((c_pat + 1))" ]
}

function allowed_force_versions() {
  # Print the two valid single-step --force targets for a given current version.
  local maj min pat
  maj="$(semver_major "${1}")"; min="$(semver_minor "${1}")"; pat="$(semver_patch "${1}")"
  printf 'v%s.%s.%s (patch) or v%s.%s.0 (minor)' "${maj}" "${min}" "$((pat + 1))" "${maj}" "$((min + 1))"
}

# validate_force_post1: enforce single-step bump rules for current >= 1.0.0.
# Major bump is only accepted when force exactly matches the auto-computed version.
# Minor bump must be exactly cur_maj.(cur_min+1).0.
# Patch bump must be exactly cur_maj.cur_min.(cur_pat+1).
function validate_force_post1() {
  local force="${1}" auto="${2}" cur="${4}"
  local force_maj cur_maj
  force_maj="$(semver_major "${force}")"
  cur_maj="$(semver_major "${cur}")"
  if [ "${force_maj}" -gt "${cur_maj}" ]; then
    [ "${force}" = "${auto}" ] && return 0
    log "ERROR: major bump via --force requires breaking changes; computed version is v${auto}"
    exit 1
  fi
  if is_single_minor_bump "${force}" "${cur}"; then return 0; fi
  if is_single_patch_bump "${force}" "${cur}"; then return 0; fi
  log "ERROR: --force v${force} is not a valid single-step bump from v${cur}"
  log "       Allowed: $(allowed_force_versions "${cur}")"
  exit 1
}

function validate_force_pre1() {
  # Pre-1.0: --force may only target 0.x.y or exactly v1.0.0.
  local force_maj force_min force_pat
  force_maj="$(semver_major "${1}")"
  force_min="$(semver_minor "${1}")"
  force_pat="$(semver_patch "${1}")"
  if [ "${force_maj}" -eq 0 ]; then return 0; fi
  if [ "${force_maj}" -eq 1 ] && [ "${force_min}" -eq 0 ] && [ "${force_pat}" -eq 0 ]; then return 0; fi
  log "ERROR: pre-1.0 --force accepts only 0.x.y or v1.0.0 (got v${1})"
  exit 1
}

function validate_force_version() {
  local force="${1}" auto="${2}" cur="${3}" bump="${4}" removed="${5}"
  if ! semver_gt "${force}" "${cur}"; then
    log "ERROR: --force v${force} must be greater than current v${cur}"
    exit 1
  fi
  local cur_maj
  cur_maj="$(semver_major "${cur}")"
  if [ "${cur_maj}" -eq 0 ]; then
    validate_force_pre1 "${force}"
    return 0
  fi
  validate_force_post1 "${force}" "${auto}" "${removed}" "${cur}"
}

function resolve_version() {
  local force="${1}" auto="${2}" cur="${3}" bump="${4}" removed="${5}"
  if [ -z "${force}" ]; then
    printf '%s' "${auto}"
    return 0
  fi
  validate_force_version "${force}" "${auto}" "${cur}" "${bump}" "${removed}"
  printf '%s' "${force}"
}

function count_fields() {
  printf '%s\n' "${1}" | grep -c '[^[:space:]]' || true
}

function build_tag_subject() {
  local bump="${1}" removed="${2}" added="${3}"
  local n
  case "${bump}" in
    major) n="$(count_fields "${removed}")"; printf 'removed %s config field(s)' "${n}" ;;
    minor) n="$(count_fields "${added}")";   printf 'added %s config field(s)' "${n}"   ;;
    patch) printf 'internal improvements; no config surface changes'                    ;;
  esac
}

function format_field_list() {
  local label="${1}" fields="${2}"
  [ -z "${fields}" ] && return 0
  printf '%s\n' "${label}"
  while IFS= read -r f; do
    [ -z "${f}" ] && continue
    printf '  %s\n' "${f}"
  done <<< "${fields}"
}

function build_tag_header() {
  local bump="${1}" cur="${2}" new="${3}"
  local commit
  commit="$(git rev-parse --short HEAD)"
  printf 'Bump level:  %s\n' "${bump}"
  printf 'Previous:    v%s\n' "${cur}"
  printf 'New version: v%s\n' "${new}"
  printf 'Commit:      %s\n' "${commit}"
}

function build_tag_footer() {
  printf 'Generated-by: scripts/release/create-release.sh\n'
  printf 'Schema:       pkg/config/schema.yaml\n'
}

function build_tag_body() {
  local bump="${1}" removed="${2}" added="${3}" cur="${4}" new="${5}"
  build_tag_header "${bump}" "${cur}" "${new}"
  printf '\n'
  format_field_list 'Fields removed (BREAKING):' "${removed}"
  format_field_list 'Fields added:' "${added}"
  printf '\n'
  build_tag_footer
}

function build_tag_message() {
  local bump="${1}" removed="${2}" added="${3}" cur="${4}" new="${5}"
  local subject body
  subject="$(build_tag_subject "${bump}" "${removed}" "${added}")"
  body="$(build_tag_body "${bump}" "${removed}" "${added}" "${cur}" "${new}")"
  printf '%s: %s\n\n%s\n' "${bump}" "${subject}" "${body}"
}

function print_field_list() {
  local label="${1}" fields="${2}"
  if [ -n "${fields}" ]; then
    log "${label}"
    while IFS= read -r field; do
      [ -z "${field}" ] && continue
      log "    ${field}"
    done <<< "${fields}"
  else
    log "${label} none"
  fi
}

function present_summary() {
  local cur="${1}" proposed="${2}" removed="${3}" added="${4}" bump="${5}"
  local subject
  subject="$(build_tag_subject "${bump}" "${removed}" "${added}")"
  log "------------------------------------------------------------------------"
  log "Keel Release: v${cur}  ->  v${proposed}"
  log "------------------------------------------------------------------------"
  print_field_list "  Fields removed (BREAKING):" "${removed}"
  print_field_list "  Fields added:" "${added}"
  log "  Bump level : ${bump}"
  log "  Tag subject: ${bump}: ${subject}"
  log ""
}

function prompt_approval() {
  local proposed="${1}"
  printf 'Tag and push %s? [y/N] ' "${proposed}" >&5
  local answer
  read -r answer </dev/tty
  if [ "${answer}" != 'y' ] && [ "${answer}" != 'Y' ]; then
    log "Release cancelled."
    exit 0
  fi
}

function create_tag() {
  local version="${1}" msg="${2}"
  git tag -s "v${version}" -m "${msg}"
  log "Tagged v${version}"
}

function push_tag() {
  local version="${1}"
  git push origin "v${version}"
  log "Pushed v${version} to origin"
}

# run_release is the main orchestration function; longer than 10 lines is
# genuinely necessary to sequence the diff, bump, and tagging logic.
function run_release() {
  local force="${1}"

  local cur_tag cur_ver
  cur_tag="$(current_git_tag)"
  cur_ver="$(strip_v "${cur_tag}")"
  log "Current version: v${cur_ver}"

  local old_fields new_fields
  old_fields="$(schema_fields_at_tag "${cur_tag}")"
  new_fields="$(schema_fields_at_head)"

  local removed added bump auto_ver
  removed="$(removed_fields "${old_fields}" "${new_fields}")"
  added="$(added_fields "${old_fields}" "${new_fields}")"
  bump="$(detect_bump "${removed}" "${added}")"
  auto_ver="$(compute_version "${cur_ver}" "${bump}")"

  local proposed
  proposed="$(resolve_version "${force}" "${auto_ver}" "${cur_ver}" "${bump}" "${removed}")"

  present_summary "${cur_ver}" "${proposed}" "${removed}" "${added}" "${bump}"
  prompt_approval "v${proposed}"

  local msg
  msg="$(build_tag_message "${bump}" "${removed}" "${added}" "${cur_ver}" "${proposed}")"
  create_tag "${proposed}" "${msg}"
  push_tag "${proposed}"
}

main "${@:-}"