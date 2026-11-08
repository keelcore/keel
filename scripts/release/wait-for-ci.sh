#!/usr/bin/env bash
# wait-for-ci.sh
# Wait for the GitHub Actions runs on a commit to finish, printing progress, then fail if any is
# red. A tag's workflow runs it first, so nothing is published from a red commit; it leaves out its
# own run and the tag's other runs. The host never waits: create-release.sh only pushes the tag.
#
# Usage: wait-for-ci.sh [COMMIT]   (default: HEAD)
#
# Env: GITHUB_REPOSITORY (else config/release-repo.local.txt), CI_WAIT_SECONDS (interval; default 30).
# Project hook: wait-for-ci.local.sh runs first, when present, with this script's arguments
# (scripts/lib/local-hook.sh: the same path, in the repository this runs in).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/local-hook.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/local-hook.sh"

# shellcheck source=../lib/tool-config.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../lib/tool-config.sh"

readonly MAX_EMPTY_LOOKS=10
readonly MAX_READ_FAILURES=10
readonly WAIT_SECONDS="${CI_WAIT_SECONDS:-30}"
COMMIT=''

function main() {
  exec 5>&1
  parse_args "$@"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  [ -n "${COMMIT}" ] || COMMIT="$(git rev-parse HEAD)"
  local -r repo="$(release_repo)" started="${SECONDS}"
  local body runs total running empties=0 failures=0
  while true; do
    if ! body="$(gh api "repos/${repo}/actions/runs?head_sha=${COMMIT}&per_page=100" 2> /dev/null)"; then
      failures=$(( failures + 1 ))
      if [ "${failures}" -ge "${MAX_READ_FAILURES}" ]; then
        log "❌ could not read CI results for ${COMMIT}"
        exit 1
      fi
      sleep "${WAIT_SECONDS}"
      continue
    fi
    failures=0
    runs="$(other_runs <<< "${body}")"
    total="$(jq -s 'length' <<< "${runs}")"
    if [ "${total}" -eq 0 ]; then
      empties=$(( empties + 1 ))
      if [ "${empties}" -ge "${MAX_EMPTY_LOOKS}" ]; then
        log "❌ no CI results for ${COMMIT}"
        exit 1
      fi
      sleep "${WAIT_SECONDS}"
      continue
    fi
    running="$(names 'select(.status != "completed")' <<< "${runs}")"
    [ -n "${running}" ] || break
    log "⏳ $(elapsed "${started}") CI on ${COMMIT}: $(( total - $(jq -s '[.[] | select(.status != "completed")] | length' <<< "${runs}") )) of ${total} finished; running: ${running}"
    sleep "${WAIT_SECONDS}"
  done
  report "${runs}" "${total}"
}

function log() {
  printf '%s\n' "${1:-}" >&5
}

function parse_args() {
  while [ "${#}" -gt 0 ]; do
    case "${1}" in
      '') ;;
      -*)
        log "❌ Error: Unexpected argument: ${1}"
        exit 1 ;;
      *) COMMIT="${1}" ;;
    esac
    shift
  done
}

# other_runs: reads the API reply; one JSON object per run, leaving out this workflow's own run and,
# in a tag's workflow, the runs of that tag.
function other_runs() {
  local tag=''
  [ "${GITHUB_REF_TYPE:-}" != 'tag' ] || tag="${GITHUB_REF_NAME:-}"
  jq -c --argjson self "${GITHUB_RUN_ID:-0}" --arg tag "${tag}" \
    '.workflow_runs[] | select(.id != $self) | select($tag == "" or .head_branch != $tag)'
}

# names JQ_FILTER: reads runs; the names of the runs matching the filter, comma-separated.
function names() {
  jq -rs "[.[] | ${1} | .name] | unique | join(\", \")"
}

function elapsed() {
  local -r seconds=$(( SECONDS - ${1} ))
  printf '%dm%02ds' "$(( seconds / 60 ))" "$(( seconds % 60 ))"
}

function report() {
  local -r runs="${1}" total="${2}"
  local red
  red="$(names 'select(.conclusion != "success" and .conclusion != "skipped" and .conclusion != "neutral")' <<< "${runs}")"
  if [ -n "${red}" ]; then
    log "❌ CI failed on ${COMMIT}: ${red}"
    exit 1
  fi
  log "✅ CI passed on ${COMMIT}: ${total} workflow runs"
}

main "$@"
