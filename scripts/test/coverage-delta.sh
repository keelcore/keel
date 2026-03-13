#!/usr/bin/env bash
# coverage-delta.sh
# Two-pass coverage gate for pull requests.
# Measures coverage at BASE_SHA (the merge target), then at the merged HEAD,
# and fails if the delta drops below COVERAGE_THRESHOLD percent.
#
# Required environment variables:
#   BASE_SHA            — base branch commit (github.event.pull_request.base.sha)
#   HEAD_SHA            — PR head commit    (github.event.pull_request.head.sha)
#
# Optional environment variables:
#   COVERAGE_THRESHOLD  — maximum allowed drop in percentage points (default: 1.0)

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly COVERAGE_BASE_FILE='/tmp/keel_coverage_base.txt'
readonly COVERAGE_PR_FILE='/tmp/keel_coverage_pr.txt'
readonly LOG_FILE='/tmp/keel_coverage_delta.log'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  require_env
  # Capture the merge commit SHA now (actions/checkout gives us refs/pull/N/merge
  # — base + PR already merged). This is what we measure for the PR side.
  local -r merge_sha="$(git rev-parse HEAD)"
  local -r threshold="${COVERAGE_THRESHOLD:-1.0}"
  log "Coverage delta check (threshold: -${threshold}%)"
  log "  Merge commit  : ${merge_sha}"
  log "  Base commit   : ${BASE_SHA}"
  local base_cov merged_cov
  base_cov="$(measure_coverage "${BASE_SHA}"  "${COVERAGE_BASE_FILE}" 'base')"
  merged_cov="$(measure_coverage "${merge_sha}" "${COVERAGE_PR_FILE}"  'merged PR')"
  report "${base_cov}" "${merged_cov}" "${threshold}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a "${LOG_FILE}" >&5
}

function validate_args() { :; }

function require_env() {
  local missing=0
  for var in BASE_SHA; do
    if [ -z "${!var:-}" ]; then
      log "ERROR: ${var} is required"
      missing=1
    fi
  done
  [ "${missing}" -eq 0 ] || exit 1
}

# measure_coverage checks out sha, runs tests, returns the total coverage %.
function measure_coverage() {
  local -r sha="${1}"
  local -r outfile="${2}"
  local -r label="${3}"
  log "  Measuring coverage at ${label} (${sha})"
  git checkout --quiet "${sha}"
  go test \
    -coverprofile="${outfile}" \
    -covermode=atomic \
    -coverpkg=./... \
    ./... \
    > /dev/null 2>&1
  extract_pct "${outfile}"
}

# extract_pct prints the total coverage percentage from a coverprofile.
function extract_pct() {
  local -r file="${1}"
  go tool cover -func="${file}" \
    | grep '^total:' \
    | awk '{print $3}' \
    | tr -d '%'
}

function report() {
  local -r base="${1}"
  local -r pr="${2}"
  local -r threshold="${3}"
  local delta
  delta="$(awk "BEGIN { printf \"%.1f\", ${pr} - ${base} }")"
  log "  Base coverage : ${base}%"
  log "  PR coverage   : ${pr}%"
  log "  Delta         : ${delta}%"
  write_step_summary "${base}" "${pr}" "${delta}"
  local failed
  failed="$(awk "BEGIN { print (${delta} < -${threshold}) ? 1 : 0 }")"
  if [ "${failed}" -eq 1 ]; then
    log "FAIL: coverage dropped ${delta}% (threshold: -${threshold}%)"
    exit 1
  fi
  log "OK: coverage delta within threshold"
}

function write_step_summary() {
  [ -z "${GITHUB_STEP_SUMMARY:-}" ] && return 0
  local -r base="${1}" pr="${2}" delta="${3}"
  {
    printf '## Coverage Delta\n\n'
    printf '| | Coverage |\n|---|---|\n'
    printf '| Base (`%s`) | %s%% |\n' "${BASE_SHA:0:7}" "${base}"
    printf '| PR (`%s`) | %s%% |\n'   "${HEAD_SHA:0:7}" "${pr}"
    printf '| **Delta** | **%s%%** |\n' "${delta}"
  } >> "${GITHUB_STEP_SUMMARY}"
}

main "${@:-}"
