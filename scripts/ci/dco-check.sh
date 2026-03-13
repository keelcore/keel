#!/usr/bin/env bash
# dco-check.sh
# Verify every commit in a pull request carries a Signed-off-by trailer.
#
# Required env vars (set automatically by the DCO workflow):
#   BASE_SHA — merge-base commit (exclusive lower bound)
#   HEAD_SHA — PR head commit (inclusive upper bound)
#
# Exit 0 if every commit has at least one Signed-off-by line.
# Exit 1 and print offending commits if any are missing it.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly LOG_FILE='/tmp/keel_dco_check.log'

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a "${LOG_FILE}"
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    log 'Usage: dco-check.sh  (set BASE_SHA and HEAD_SHA env vars)'
    exit 1
  fi
}

function require_env() {
  local -r var="${1}"
  if [ -z "${!var:-}" ]; then
    log "❌ Error: ${var} is not set"
    exit 1
  fi
}

function check_commits() {
  local base="${1}" head="${2}"
  local missing=0
  local sha subject

  while IFS=' ' read -r sha subject; do
    [ -z "${sha}" ] && continue
    local body
    body="$(git log -1 --format='%b' "${sha}")"
    if ! printf '%s\n' "${body}" | grep -qi '^Signed-off-by:'; then
      log "❌  ${sha}  ${subject}"
      log "    Missing: Signed-off-by: Your Name <your@email.com>"
      missing=1
    fi
  done < <(git log --format='%h %s' "${base}..${head}")

  return "${missing}"
}

function main() {
  validate_args "${@:-}"
  require_env 'BASE_SHA'
  require_env 'HEAD_SHA'

  log "DCO check: ${BASE_SHA:0:7}..${HEAD_SHA:0:7}"
  local failed=0
  check_commits "${BASE_SHA}" "${HEAD_SHA}" || failed=1

  if [ "${failed}" -eq 1 ]; then
    log ""
    log "One or more commits are missing a DCO Signed-off-by trailer."
    log ""
    log "To sign off new commits:"
    log "  git commit -s -m 'type(scope): description'"
    log ""
    log "To retroactively sign off all commits in this PR (replace N):"
    log "  git rebase --signoff HEAD~N && git push --force-with-lease"
    exit 1
  fi

  log "✅  All commits are signed off."
}

main "${@:-}"
