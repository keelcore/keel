#!/usr/bin/env bash
# pr-policy.sh
# Validate PR compliance before human code review begins.
# This script is a required CI gate AND runnable locally before pushing.
#
# In CI, pass context via environment variables (set by the workflow):
#   PR_TITLE        — pull request title
#   PR_BODY         — pull request description
#   GITHUB_HEAD_REF — source branch name
#   BASE_SHA        — base commit SHA
#   HEAD_SHA        — head commit SHA
#
# Run locally (no env vars needed — reads from git):
#   ./scripts/ci/pr-policy.sh

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# ---------------------------------------------------------------------------
# Policy configuration
# ---------------------------------------------------------------------------

readonly MIN_TITLE_CHARS=10
readonly MIN_BODY_CHARS=30
readonly MAX_FILE_SIZE_WARN=5242880    # 5 MB — warn but do not fail
readonly MAX_FILE_SIZE_HARD=10485760   # 10 MB — hard failure

# Conventional commit types allowed in PR title and commit messages.
readonly CONVENTIONAL_COMMIT_TYPES='feat|fix|chore|docs|test|refactor|perf|ci|build|release|hotfix|revert'

# Issue-exempt PR types (these may omit a linked issue).
readonly ISSUE_EXEMPT_TYPES='chore|docs|test|ci|build|refactor|release'

# Allowed branch name prefixes. The default branch is always exempt.
readonly ALLOWED_BRANCH_PREFIXES=(
  'feat/'
  'fix/'
  'chore/'
  'docs/'
  'test/'
  'refactor/'
  'perf/'
  'ci/'
  'build/'
  'release/'
  'hotfix/'
  'dependabot/'
  'revert/'
)

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

function main() {
  exec 5>&1
  local failed=0
  log "PR policy compliance checks"
  log "-----------------------------"
  run_checks || failed=1
  log "-----------------------------"
  report_result "${failed}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_pr_policy.log' >&5
}

function err() {
  log "POLICY ERROR: ${1:-}"
}

function run_checks() {
  local failed=0
  check_pr_title         || failed=1
  check_pr_body          || failed=1
  check_branch_name      || failed=1
  check_file_sizes       || failed=1
  check_commit_messages  || failed=1
  check_linked_issue     || failed=1
  return "${failed}"
}

function report_result() {
  local -r failed="${1}"
  if [ "${failed}" -eq 1 ]; then
    log "FAIL: one or more policy checks failed — resolve before requesting review"
    exit 1
  fi
  log "PASS: all policy checks passed"
}

# ---------------------------------------------------------------------------
# Checks
# ---------------------------------------------------------------------------

function check_pr_title() {
  local title
  title="$(resolve_title)"
  [ -z "${title}" ] && log "  [SKIP] title: no title available (not in PR context)" && return 0
  validate_title_length "${title}" || return 1
  validate_title_format "${title}" || return 1
  log "  [PASS] title: '${title}'"
}

function resolve_title() {
  local title="${PR_TITLE:-}"
  [ -z "${title}" ] && title="$(git log -1 --pretty=%s 2>/dev/null || true)"
  printf '%s' "${title}"
}

function validate_title_length() {
  local -r title="${1}"
  if [ "${#title}" -lt "${MIN_TITLE_CHARS}" ]; then
    err "PR title too short (${#title} chars; minimum ${MIN_TITLE_CHARS}): '${title}'"
    return 1
  fi
}

function validate_title_format() {
  local -r title="${1}"
  if ! [[ "${title}" =~ ^(${CONVENTIONAL_COMMIT_TYPES})(\(.+\))?: ]]; then
    err "PR title must follow Conventional Commits: type(scope): description"
    err "  Allowed types: ${CONVENTIONAL_COMMIT_TYPES}"
    return 1
  fi
}

function check_pr_body() {
  local -r body="${PR_BODY:-}"
  [ -z "${body}" ] && log "  [SKIP] description: not in PR context" && return 0
  local -r length="${#body}"
  if [ "${length}" -lt "${MIN_BODY_CHARS}" ]; then
    err "PR description too short (${length} chars; minimum ${MIN_BODY_CHARS})"
    err "  Add context: motivation, what changed, how to test."
    return 1
  fi
  log "  [PASS] description: ${length} chars"
}

function check_branch_name() {
  local branch
  branch="$(resolve_branch)"
  [ -z "${branch}" ] && log "  [SKIP] branch name: cannot determine branch" && return 0
  is_default_branch "${branch}" && log "  [PASS] branch name: '${branch}'" && return 0
  is_allowed_branch "${branch}" && log "  [PASS] branch name: '${branch}'" && return 0
  err "Branch '${branch}' does not match a required prefix."
  err "  Allowed: ${ALLOWED_BRANCH_PREFIXES[*]}"
  return 1
}

function resolve_branch() {
  local branch="${GITHUB_HEAD_REF:-}"
  [ -z "${branch}" ] && branch="$(git branch --show-current 2>/dev/null || true)"
  printf '%s' "${branch}"
}

function is_default_branch() {
  [ "${1}" = 'main' ] || [ "${1}" = 'master' ]
}

function is_allowed_branch() {
  local -r branch="${1}"
  local prefix
  for prefix in "${ALLOWED_BRANCH_PREFIXES[@]}"; do
    [[ "${branch}" == "${prefix}"* ]] && return 0
  done
  return 1
}

function check_file_sizes() {
  local base
  base="$(resolve_base_sha)"
  [ -z "${base}" ] && log "  [SKIP] file sizes: cannot determine base commit" && return 0
  local -r head="${HEAD_SHA:-HEAD}"
  local failed=0
  while IFS= read -r file; do
    check_one_file_size "${file}" || failed=1
  done < <(git diff --name-only "${base}" "${head}" -- 2>/dev/null || true)
  [ "${failed}" -eq 0 ] && log "  [PASS] file sizes"
  return "${failed}"
}

function resolve_base_sha() {
  local base="${BASE_SHA:-}"
  [ -z "${base}" ] && base="$(git merge-base HEAD main 2>/dev/null || true)"
  printf '%s' "${base}"
}

function check_one_file_size() {
  local -r file="${1}"
  [ -z "${file}" ] && return 0
  [ -f "${file}" ]  || return 0
  local size
  size="$(wc -c < "${file}" | tr -d '[:space:]')"
  if [ "${size}" -gt "${MAX_FILE_SIZE_HARD}" ]; then
    err "File '${file}' is $(( size / 1048576 )) MB — exceeds hard limit of $(( MAX_FILE_SIZE_HARD / 1048576 )) MB"
    return 1
  fi
  if [ "${size}" -gt "${MAX_FILE_SIZE_WARN}" ]; then
    log "  [WARN] file '${file}' is $(( size / 1048576 )) MB — consider whether it belongs in the repository"
  fi
}

function check_commit_messages() {
  local base
  base="$(resolve_base_sha)"
  [ -z "${base}" ] && log "  [SKIP] commit messages: cannot determine base commit" && return 0
  local -r head="${HEAD_SHA:-HEAD}"
  local failed=0
  while IFS= read -r subject; do
    validate_commit_subject "${subject}" || failed=1
  done < <(git log --pretty=%s "${base}..${head}" 2>/dev/null || true)
  [ "${failed}" -eq 0 ] && log "  [PASS] commit messages"
  return "${failed}"
}

function validate_commit_subject() {
  local -r subject="${1}"
  [ -z "${subject}" ] && return 0
  if ! [[ "${subject}" =~ ^(${CONVENTIONAL_COMMIT_TYPES})(\(.+\))?: ]]; then
    err "Commit '${subject}' does not follow Conventional Commits format"
    return 1
  fi
}

function check_linked_issue() {
  local -r body="${PR_BODY:-}"
  [ -z "${body}" ] && log "  [SKIP] linked issue: not in PR context" && return 0
  local title
  title="$(resolve_title)"
  is_issue_exempt_type "${title}" && log "  [SKIP] linked issue: exempt type" && return 0
  if has_issue_reference "${body}"; then
    log "  [PASS] linked issue"
    return 0
  fi
  err "PR body must reference an issue: 'Closes #N', 'Fixes #N', or 'Refs #N'"
  return 1
}

function is_issue_exempt_type() {
  local -r title="${1}"
  [[ "${title}" =~ ^(${ISSUE_EXEMPT_TYPES})(\(.+\))?: ]]
}

function has_issue_reference() {
  local -r body="${1}"
  [[ "${body}" =~ (Closes|Fixes|Resolves|Refs)[[:space:]]+#[0-9]+ ]]
}

main "${@:-}"
