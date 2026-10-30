#!/usr/bin/env bash
# create-release.sh
# Canonical release tagger. Git tags are the only version source: this script
# computes the next semver tag from the previous tag and a bump level, presents
# the findings for approval, then creates a signed annotated tag and pushes
# that one tag. Developer action only — CI never tags (governance/ci.md).
#
# The bump level comes from the project-owned hook
#   scripts/release/create-release.local.sh <previous-tag>
# resolved from the git toplevel of the repo being released, so a consumer's
# own hook runs, never the copy inside its .standards submodule. Hooks are not
# canonical and may differ per repo. The hook prints two lines to stdout:
#   1. bump level: major | minor | patch
#   2. one-line tag subject (empty means "release vX.Y.Z")
# Anything it writes to stderr is shown as findings and recorded in the tag
# body. A non-zero exit aborts the release. With no hook the bump is patch.
#
# While the major version is 0 the bump is demoted one step (major -> minor,
# minor -> patch, patch -> patch only if there are commits since the tag).
#
# Usage:
#   create-release.sh [--force vX.Y.Z] [--dry-run]
#     --force vX.Y.Z  Use this version instead of the computed one, validated
#                     per governance/ci.md "--force Override Rules".
#     --dry-run       Compute and print the version and tag message; never
#                     prompt, tag or push. Failed preconditions are warnings.
#
# Env (testing only):
#   CREATE_RELEASE_TTY  file the approval answer is read from (default /dev/tty)

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

declare -r LOG_FILE='/tmp/create_release.log'
declare -r HOOK_REL='scripts/release/create-release.local.sh'
declare -r DEMOTION_NOTE=' (pre-1.0 demotion)'
declare -rx CREATE_RELEASE_TTY="${CREATE_RELEASE_TTY:-/dev/tty}"
declare FINDINGS_FILE
FINDINGS_FILE="$(mktemp "${TMPDIR:-/tmp}/create_release_findings.XXXXXX")"

function main() {
  exec 5>&1
  trap cleanup EXIT
  validate_args "${@:-}"
  local force dry_run
  force="$(parse_force "${@:-}")"
  dry_run="$(parse_dry_run "${@:-}")"
  ensure_repo_root
  check_preconditions "${dry_run}"
  run_release "${force}" "${dry_run}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a "${LOG_FILE}" >&5
}

function cleanup() {
  rm -f "${FINDINGS_FILE}"
}

# Accepts any order of: (nothing), --dry-run, --force vX.Y.Z. Empty arguments
# (the GitHub Actions "${@:-}" artifact) are ignored.
function validate_args() {
  while [ "${#}" -gt 0 ]; do
    case "${1}" in
      '' | --dry-run) shift ;;
      --force)
        validate_version_arg "${2:-}"
        shift 2
        ;;
      *) usage_error ;;
    esac
  done
}

function validate_version_arg() {
  if ! [[ "${1}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    log '❌ Error: --force needs a version of the form vX.Y.Z (e.g. v1.2.3)'
    exit 1
  fi
}

function usage_error() {
  log '❌ Usage: create-release.sh [--force vX.Y.Z] [--dry-run]'
  exit 1
}

function parse_force() {
  while [ "${#}" -gt 0 ]; do
    if [ "${1}" = '--force' ]; then
      strip_v "${2}"
      return 0
    fi
    shift
  done
}

function parse_dry_run() {
  local arg
  for arg in "${@:-}"; do
    if [ "${arg}" = '--dry-run' ]; then
      printf '1'
      return 0
    fi
  done
  printf '0'
}

function ensure_repo_root() {
  local root
  root="$(git rev-parse --show-toplevel)"
  cd "${root}"
}

function check_preconditions() {
  local -r dry_run="${1}"
  log '🔍 Checking release preconditions...'
  check_branch || precondition_failed "${dry_run}"
  check_upstream || precondition_failed "${dry_run}"
  check_head_matches_upstream || precondition_failed "${dry_run}"
  check_clean_tree || precondition_failed "${dry_run}"
  log '✅ Preconditions checked'
}

function precondition_failed() {
  local -r dry_run="${1}"
  if [ "${dry_run}" = '1' ]; then
    log '⚠️  Dry run: continuing despite the failed precondition above'
    return 0
  fi
  exit 1
}

function check_branch() {
  local branch
  branch="$(git rev-parse --abbrev-ref HEAD)"
  if [ "${branch}" != 'main' ]; then
    log "❌ must be on main branch (currently on '${branch}')"
    return 1
  fi
}

function check_upstream() {
  if ! git rev-parse --abbrev-ref --symbolic-full-name '@{upstream}' >/dev/null 2>&1; then
    log '❌ no upstream configured for this branch'
    log '   Run: git branch --set-upstream-to=<remote>/main main'
    return 1
  fi
}

# With no upstream there is nothing to compare; check_upstream reported it.
function check_head_matches_upstream() {
  local local_sha upstream_sha
  git rev-parse '@{upstream}' >/dev/null 2>&1 || return 0
  fetch_upstream || return 1
  local_sha="$(git rev-parse HEAD)"
  upstream_sha="$(git rev-parse '@{upstream}')"
  if [ "${local_sha}" != "${upstream_sha}" ]; then
    log "❌ local HEAD (${local_sha:0:7}) differs from upstream (${upstream_sha:0:7})"
    log '   Run: git pull --ff-only'
    return 1
  fi
}

function fetch_upstream() {
  log '🔄 Fetching from upstream...'
  if ! git fetch --quiet; then
    log '❌ git fetch failed'
    return 1
  fi
}

function check_clean_tree() {
  if ! git diff --quiet || ! git diff --cached --quiet; then
    log '❌ working tree is not clean; commit or stash changes first'
    return 1
  fi
}

function run_release() {
  local -r force="${1}" dry_run="${2}"
  local cur_tag cur_ver hook_out
  cur_tag="$(current_git_tag)"
  cur_ver="$(strip_v "${cur_tag}")"
  log "🏷️  Current version: v${cur_ver}"
  hook_out="$(bump_from_hook "${cur_tag}")"
  release_from "${cur_tag}" "${cur_ver}" "${hook_out}" "${force}" "${dry_run}"
}

function current_git_tag() {
  local tag
  if ! tag="$(git describe --tags --abbrev=0 2>/dev/null)"; then
    log '❌ no tags found in repository'
    log "   Create an initial tag first: git tag -s v0.1.0 -m 'initial release'"
    exit 1
  fi
  printf '%s' "${tag}"
}

# Prints the hook's two lines (bump, subject); exits non-zero on any failure.
function bump_from_hook() {
  local -r tag="${1}"
  local out
  if ! out="$(run_hook "${tag}")"; then
    show_findings
    log "❌ ${HOOK_REL} failed; release aborted"
    exit 1
  fi
  validate_hook_output "${out}"
  printf '%s\n' "${out}"
}

function run_hook() {
  local -r tag="${1}"
  local hook
  hook="$(git rev-parse --show-toplevel)/${HOOK_REL}"
  if [ ! -f "${hook}" ]; then
    log "ℹ️  No ${HOOK_REL} in this repo; defaulting to a patch bump"
    printf 'patch\n'
    return 0
  fi
  log "🔧 Running ${HOOK_REL} ${tag}"
  bash "${hook}" "${tag}" 2>"${FINDINGS_FILE}"
}

function validate_hook_output() {
  local -r out="${1}"
  check_hook_line_count "${out}"
  check_hook_bump "$(printf '%s\n' "${out}" | sed -n '1p')"
}

function check_hook_line_count() {
  local -r out="${1}"
  local lines
  lines="$(printf '%s\n' "${out}" | wc -l | tr -d ' ')"
  if [ "${lines}" -gt 2 ]; then
    log "❌ ${HOOK_REL} printed ${lines} lines; expected 2 (bump, subject)"
    exit 1
  fi
}

function check_hook_bump() {
  local -r bump="${1}"
  case "${bump}" in
    major | minor | patch) ;;
    *)
      log "❌ ${HOOK_REL} printed bump '${bump}'; expected major, minor or patch"
      exit 1
      ;;
  esac
}

function release_from() {
  local -r cur_tag="${1}" cur_ver="${2}" hook_out="${3}" force="${4}" dry_run="${5}"
  local raw_bump bump auto version subject
  raw_bump="$(printf '%s\n' "${hook_out}" | sed -n '1p')"
  bump="$(effective_bump "${cur_tag}" "${cur_ver}" "${raw_bump}")"
  stop_if_nothing_to_release "${bump}" "${cur_tag}"
  auto="$(compute_version "${cur_ver}" "${bump}")"
  version="$(resolve_version "${force}" "${auto}" "${cur_ver}")"
  subject="$(final_subject "$(printf '%s\n' "${hook_out}" | sed -n '2p')" "${raw_bump}" "${bump}" "${version}")"
  present_summary "${cur_ver}" "${version}" "${bump}" "${subject}"
  tag_or_preview "${version}" "$(build_tag_message "${bump}" "${subject}" "${cur_ver}" "${version}")" "${dry_run}"
}

function effective_bump() {
  local -r cur_tag="${1}" cur_ver="${2}" raw="${3}"
  local bump
  bump="$(pre1_demote_bump "${cur_ver}" "${raw}")"
  if [ "${bump}" = 'none' ] && [ "$(git rev-list --count "${cur_tag}..HEAD")" -gt 0 ]; then
    bump='patch'
  fi
  printf '%s' "${bump}"
}

function stop_if_nothing_to_release() {
  local -r bump="${1}" cur_tag="${2}"
  if [ "${bump}" = 'none' ]; then
    log "No commits since ${cur_tag}; nothing to release. Exiting."
    exit 0
  fi
}

function strip_v() {
  printf '%s' "${1}" | sed 's/^v//; s/[-+].*//'
}

function pre1_demote_bump() {
  # When major version is 0, demote the bump level by one step.
  # major -> minor, minor -> patch, patch -> none (no tag created).
  # When major >= 1 the bump passes through unchanged.
  local -r cur_ver="${1}" bump="${2}"
  if [ "$(semver_major "${cur_ver}")" -ge 1 ]; then
    printf '%s' "${bump}"
    return 0
  fi
  case "${bump}" in
    major) printf 'minor' ;;
    minor) printf 'patch' ;;
    patch) printf 'none' ;;
  esac
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
  local -r a="${1}" b="${2}"
  local a_maj a_min a_pat b_maj b_min b_pat
  IFS='.' read -r a_maj a_min a_pat <<< "${a}"
  IFS='.' read -r b_maj b_min b_pat <<< "${b}"
  if [ "${a_maj}" -ne "${b_maj}" ]; then
    [ "${a_maj}" -gt "${b_maj}" ]
  elif [ "${a_min}" -ne "${b_min}" ]; then
    [ "${a_min}" -gt "${b_min}" ]
  else
    [ "${a_pat}" -gt "${b_pat}" ]
  fi
}

function compute_version() {
  local -r cur="${1}" bump="${2}"
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
  local -r force="${1}" cur="${2}"
  [ "$(semver_major "${force}")" -eq "$(semver_major "${cur}")" ] \
    && [ "$(semver_minor "${force}")" -eq "$(($(semver_minor "${cur}") + 1))" ] \
    && [ "$(semver_patch "${force}")" -eq 0 ]
}

function is_single_patch_bump() {
  # Returns 0 if force == cur_maj.cur_min.(cur_pat+1) exactly.
  local -r force="${1}" cur="${2}"
  [ "$(semver_major "${force}")" -eq "$(semver_major "${cur}")" ] \
    && [ "$(semver_minor "${force}")" -eq "$(semver_minor "${cur}")" ] \
    && [ "$(semver_patch "${force}")" -eq "$(($(semver_patch "${cur}") + 1))" ]
}

function allowed_force_versions() {
  # Print the two valid single-step --force targets for a given current version.
  local maj min pat
  maj="$(semver_major "${1}")"
  min="$(semver_minor "${1}")"
  pat="$(semver_patch "${1}")"
  printf 'v%s.%s.%s (patch) or v%s.%s.0 (minor)' "${maj}" "${min}" "$((pat + 1))" "${maj}" "$((min + 1))"
}

# validate_force_post1: enforce single-step bump rules for current >= 1.0.0.
# A major bump is only accepted when force exactly matches the computed version
# (i.e. the hook reported a major bump). Minor must be cur_maj.(cur_min+1).0;
# patch must be cur_maj.cur_min.(cur_pat+1).
function validate_force_post1() {
  local -r force="${1}" auto="${2}" cur="${3}"
  if [ "$(semver_major "${force}")" -gt "$(semver_major "${cur}")" ]; then
    [ "${force}" = "${auto}" ] && return 0
    log "❌ major bump via --force requires a breaking change; computed version is v${auto}"
    exit 1
  fi
  is_single_minor_bump "${force}" "${cur}" && return 0
  is_single_patch_bump "${force}" "${cur}" && return 0
  log "❌ --force v${force} is not a valid single-step bump from v${cur}"
  log "   Allowed: $(allowed_force_versions "${cur}")"
  exit 1
}

function validate_force_pre1() {
  # Pre-1.0: --force may only target 0.x.y or exactly v1.0.0.
  local -r force="${1}"
  if [ "$(semver_major "${force}")" -eq 0 ] || [ "${force}" = '1.0.0' ]; then
    return 0
  fi
  log "❌ pre-1.0 --force accepts only 0.x.y or v1.0.0 (got v${force})"
  exit 1
}

function validate_force_version() {
  local -r force="${1}" auto="${2}" cur="${3}"
  if ! semver_gt "${force}" "${cur}"; then
    log "❌ --force v${force} must be greater than current v${cur}"
    exit 1
  fi
  if [ "$(semver_major "${cur}")" -eq 0 ]; then
    validate_force_pre1 "${force}"
    return 0
  fi
  validate_force_post1 "${force}" "${auto}" "${cur}"
}

function resolve_version() {
  local -r force="${1}" auto="${2}" cur="${3}"
  if [ -z "${force}" ]; then
    printf '%s' "${auto}"
    return 0
  fi
  validate_force_version "${force}" "${auto}" "${cur}"
  printf '%s' "${force}"
}

# The hook describes the raw change; note when pre-1.0 demotion lowered it.
function final_subject() {
  local -r subject="${1}" raw="${2}" bump="${3}" version="${4}"
  if [ -z "${subject}" ]; then
    printf 'release v%s' "${version}"
  elif [ "${raw}" != "${bump}" ] && [ "${raw}" != 'patch' ]; then
    printf '%s%s' "${subject}" "${DEMOTION_NOTE}"
  else
    printf '%s' "${subject}"
  fi
}

function show_findings() {
  local line
  [ -s "${FINDINGS_FILE}" ] || return 0
  while IFS= read -r line; do
    log "  ${line}"
  done < "${FINDINGS_FILE}"
}

function present_summary() {
  local -r cur="${1}" new="${2}" bump="${3}" subject="${4}"
  log '------------------------------------------------------------------------'
  log "$(basename "$(pwd)") release: v${cur}  ->  v${new}"
  log '------------------------------------------------------------------------'
  show_findings
  log "  Bump level : ${bump}"
  log "  Tag subject: ${bump}: ${subject}"
  log ''
}

function build_tag_message() {
  local -r bump="${1}" subject="${2}" cur="${3}" new="${4}"
  printf '%s: %s\n\n' "${bump}" "${subject}"
  printf 'Bump level:  %s\nPrevious:    v%s\n' "${bump}" "${cur}"
  printf 'New version: v%s\nCommit:      %s\n' "${new}" "$(git rev-parse --short HEAD)"
  if [ -s "${FINDINGS_FILE}" ]; then
    printf '\n'
    cat "${FINDINGS_FILE}"
  fi
  printf '\nGenerated-by: scripts/release/create-release.sh\n'
}

function tag_or_preview() {
  local -r version="${1}" msg="${2}" dry_run="${3}"
  if [ "${dry_run}" = '1' ]; then
    log '📝 Tag message (dry run):'
    log "${msg}"
    log "✅ Dry run complete: v${version} computed; no tag created, nothing pushed"
    return 0
  fi
  prompt_approval "${version}"
  create_tag "${version}" "${msg}"
  push_tag "${version}"
}

function prompt_approval() {
  local -r version="${1}"
  local answer=''
  printf 'Tag and push v%s? [y/N] ' "${version}" >&5
  read -r answer < "${CREATE_RELEASE_TTY}" || true
  if [ "${answer}" != 'y' ] && [ "${answer}" != 'Y' ]; then
    log 'Release cancelled.'
    exit 0
  fi
}

function create_tag() {
  local -r version="${1}" msg="${2}"
  log "🔏 Creating signed annotated tag v${version}"
  git tag -s "v${version}" -m "${msg}"
  log "✅ Tagged v${version}"
}

function push_tag() {
  local -r version="${1}"
  local remote
  remote="$(git rev-parse --abbrev-ref --symbolic-full-name '@{upstream}' | cut -d/ -f1)"
  log "🚀 Pushing v${version} to ${remote}"
  git push "${remote}" "v${version}"
  log "✅ Pushed v${version} to ${remote}"
}

main "${@:-}"
