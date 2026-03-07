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

  check_pr_title    || failed=1
  check_pr_body     || failed=1
  check_branch_name || failed=1
  check_file_sizes  || failed=1

  log "-----------------------------"
  if [ "${failed}" -eq 1 ]; then
    log "FAIL: one or more policy checks failed — resolve before requesting review"
    exit 1
  fi
  log "PASS: all policy checks passed"
}

function log() {
  printf '%s\n' "${1:-}" >&5
}

function err() {
  printf 'POLICY ERROR: %s\n' "${1:-}" >&2
}

# ---------------------------------------------------------------------------
# Checks
# ---------------------------------------------------------------------------

function check_pr_title() {
  local title="${PR_TITLE:-}"

  if [ -z "${title}" ]; then
    # Local fallback: use the most recent commit subject line.
    title="$(git log -1 --pretty=%s 2>/dev/null || true)"
  fi

  if [ -z "${title}" ]; then
    log "  [SKIP] title: no title available (not in PR context)"
    return 0
  fi

  if [ "${#title}" -lt "${MIN_TITLE_CHARS}" ]; then
    err "PR title too short (${#title} chars; minimum ${MIN_TITLE_CHARS}): '${title}'"
    return 1
  fi

  log "  [PASS] title: '${title}'"
}

function check_pr_body() {
  local body="${PR_BODY:-}"

  if [ -z "${body}" ]; then
    log "  [SKIP] description: not in PR context"
    return 0
  fi

  local length="${#body}"
  if [ "${length}" -lt "${MIN_BODY_CHARS}" ]; then
    err "PR description too short (${length} chars; minimum ${MIN_BODY_CHARS})"
    err "  Add context: motivation, what changed, how to test."
    return 1
  fi

  log "  [PASS] description: ${length} chars"
}

function check_branch_name() {
  local branch="${GITHUB_HEAD_REF:-}"

  if [ -z "${branch}" ]; then
    branch="$(git branch --show-current 2>/dev/null || true)"
  fi

  if [ -z "${branch}" ]; then
    log "  [SKIP] branch name: cannot determine branch"
    return 0
  fi

  # Default branch is always allowed (shouldn't appear in a PR, but be safe).
  if [ "${branch}" = 'main' ] || [ "${branch}" = 'master' ]; then
    log "  [PASS] branch name: '${branch}'"
    return 0
  fi

  local prefix
  for prefix in "${ALLOWED_BRANCH_PREFIXES[@]}"; do
    if [[ "${branch}" == "${prefix}"* ]]; then
      log "  [PASS] branch name: '${branch}'"
      return 0
    fi
  done

  err "Branch '${branch}' does not match a required prefix."
  err "  Allowed: ${ALLOWED_BRANCH_PREFIXES[*]}"
  return 1
}

function check_file_sizes() {
  local base="${BASE_SHA:-}"
  local head="${HEAD_SHA:-HEAD}"

  if [ -z "${base}" ]; then
    # Local fallback: compare against the merge base with main.
    base="$(git merge-base HEAD main 2>/dev/null || true)"
  fi

  if [ -z "${base}" ]; then
    log "  [SKIP] file sizes: cannot determine base commit"
    return 0
  fi

  local failed=0
  local file size

  while IFS= read -r file; do
    [ -z "${file}" ] && continue
    [ -f "${file}" ] || continue

    size="$(wc -c < "${file}" | tr -d '[:space:]')"

    if [ "${size}" -gt "${MAX_FILE_SIZE_HARD}" ]; then
      err "File '${file}' is $(( size / 1048576 )) MB — exceeds hard limit of $(( MAX_FILE_SIZE_HARD / 1048576 )) MB"
      failed=1
    elif [ "${size}" -gt "${MAX_FILE_SIZE_WARN}" ]; then
      log "  [WARN] file '${file}' is $(( size / 1048576 )) MB — consider whether it belongs in the repository"
    fi
  done < <(git diff --name-only "${base}" "${head}" -- 2>/dev/null || true)

  if [ "${failed}" -eq 0 ]; then
    log "  [PASS] file sizes"
  fi
  return "${failed}"
}

main "${@:-}"