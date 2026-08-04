#!/usr/bin/env bash
# scripts/ci/pr-policy.sh
# PR policy gate. Validates PR metadata and content before code review begins.
# Exits non-zero if any policy check fails.
#
# Required environment variables (set by GitHub Actions):
#   GITHUB_HEAD_REF    — source branch name
#   PR_TITLE           — pull request title
#   PR_BODY            — pull request body
#   PR_AUTHOR          — pull request author login

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# Conventional commit types allowed in PR titles.
readonly CONVENTIONAL_TYPES='feat|fix|docs|chore|refactor|test|ci|perf|build|revert|dependabot'

# Branch naming pattern: type/short-description or username/short-description
readonly BRANCH_PATTERN='^(feat|fix|docs|chore|refactor|test|ci|perf|build|revert|dependabot|adr|rfc)/[a-z0-9-]+$'

# Minimum PR body length (characters) to reject template stubs.
readonly MIN_BODY_LENGTH=50

function main() {
  exec 5>&1
  local failures=0
  check_branch_name    || failures=$((failures + 1))
  check_pr_title       || failures=$((failures + 1))
  check_pr_body        || failures=$((failures + 1))
  check_secret_scan    || failures=$((failures + 1))
  check_file_sizes     || failures=$((failures + 1))
  summarize "${failures}"
}

function check_branch_name() {
  local -r branch="${GITHUB_HEAD_REF:-}"
  if [ -z "${branch}" ]; then
    log '⚠️  GITHUB_HEAD_REF not set; skipping branch name check'
    return 0
  fi
  if [ "${PR_AUTHOR:-}" = 'dependabot[bot]' ]; then
    log "Checking branch name: ${branch}"
    log '  ✅ Branch name valid (dependabot exemption)'
    return 0
  fi
  log "Checking branch name: ${branch}"
  if ! echo "${branch}" | grep -qP "${BRANCH_PATTERN}"; then
    log "  ❌ Branch '${branch}' does not match pattern: ${BRANCH_PATTERN}"
    return 1
  fi
  log '  ✅ Branch name valid'
}

function check_pr_title() {
  local -r title="${PR_TITLE:-}"
  if [ -z "${title}" ]; then
    log '⚠️  PR_TITLE not set; skipping title check'
    return 0
  fi
  log "Checking PR title: ${title}"
  local pattern
  pattern="^(${CONVENTIONAL_TYPES})(\(.+\))?: .{1,100}$"
  if ! echo "${title}" | grep -qP "${pattern}"; then
    log "  ❌ PR title does not follow conventional commits format"
    log "     Expected: type(scope)?: description"
    log "     Valid types: ${CONVENTIONAL_TYPES}"
    return 1
  fi
  log '  ✅ PR title valid'
}

function check_pr_body() {
  local -r body="${PR_BODY:-}"
  if [ -z "${body}" ]; then
    log '  ❌ PR body is empty; a description is required'
    return 1
  fi
  local length
  length="${#body}"
  if [ "${length}" -lt "${MIN_BODY_LENGTH}" ]; then
    log "  ❌ PR body is too short (${length} chars; minimum ${MIN_BODY_LENGTH})"
    return 1
  fi
  log "  ✅ PR body present (${length} chars)"
}

function check_secret_scan() {
  log 'Scanning staged files for secrets...'
  local rc=0
  scan_for_secrets || rc="${?}"
  if [ "${rc}" -ne 0 ]; then
    log '  ❌ Potential secrets detected; review the output above'
    return 1
  fi
  log '  ✅ No secrets detected'
}

function scan_for_secrets() {
  # Patterns that indicate hardcoded secrets. Adjust as tooling evolves.
  local -r patterns=(
    'AKIA[0-9A-Z]{16}'
    'sk-[a-zA-Z0-9]{32,}'
    'ghp_[a-zA-Z0-9]{36}'
    'ghs_[a-zA-Z0-9]{36}'
    'xox[baprs]-[0-9]{12}-[0-9]{12}-[a-zA-Z0-9]{24}'
    'BEGIN (RSA|EC|DSA|OPENSSH) PRIVATE KEY'
    'password\s*=\s*["\x27][^"\x27]{8,}'
    'secret\s*=\s*["\x27][^"\x27]{8,}'
  )
  local combined
  combined="$(printf '%s|' "${patterns[@]}")"
  combined="${combined%|}"
  git diff --name-only HEAD~1 2>/dev/null \
    | xargs -I{} grep -PnH "${combined}" {} 2>/dev/null \
    || true
  # Re-run to capture exit code from grep match
  if git diff --name-only HEAD~1 2>/dev/null \
      | xargs -I{} grep -qP "${combined}" {} 2>/dev/null; then
    return 1
  fi
}

function check_file_sizes() {
  log 'Checking file sizes...'
  local oversized=0
  local file size
  while IFS= read -r file; do
    [ -f "${file}" ] || continue
    size="$(wc -c < "${file}")"
    if [ "${size}" -gt 1048576 ]; then
      log "  ❌ ${file}: ${size} bytes exceeds 1 MB limit"
      oversized=$((oversized + 1))
    fi
  done < <(git diff --name-only HEAD~1 2>/dev/null || true)
  if [ "${oversized}" -gt 0 ]; then
    log "  ❌ ${oversized} file(s) exceed the size limit"
    return 1
  fi
  log '  ✅ File sizes within limits'
}

function summarize() {
  local -r failures="${1}"
  if [ "${failures}" -gt 0 ]; then
    log ""
    log "❌ PR policy failed: ${failures} check(s) did not pass"
    exit 1
  fi
  log ""
  log '✅ All PR policy checks passed'
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/pr-policy.log' >&5
}

main "${@:-}"
