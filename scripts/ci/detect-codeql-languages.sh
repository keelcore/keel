#!/usr/bin/env bash
# scripts/ci/detect-codeql-languages.sh
# The CodeQL languages this repository holds, as a JSON list in the workflow output `languages`
# (codeql.yml). Detected from repo-owned tracked files; config/codeql.local.txt (one language per
# line, # comments) replaces the detection.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/paths.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"

readonly OVERRIDE='config/codeql.local.txt'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local json
  json="$(languages | to_json)"
  log "languages=${json}"
  [ -z "${GITHUB_OUTPUT:-}" ] || printf 'languages=%s\n' "${json}" >> "${GITHUB_OUTPUT}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/codeql_languages.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

function languages() {
  if [ -f "${OVERRIDE}" ]; then
    sed -e 's/#.*//' -e 's/[[:space:]]*$//' "${OVERRIDE}" | grep -v '^$' || true
    return 0
  fi
  [ -z "$(source_files '*.go')" ] || printf 'go\n'
  [ -z "$(source_files '*.js' '*.mjs' '*.cjs' '*.ts' '*.tsx')" ] || printf 'javascript-typescript\n'
}

function to_json() {
  awk 'BEGIN { printf "[" } { printf "%s\"%s\"", (NR > 1 ? "," : ""), $0 } END { printf "]" }'
}

main "${@:-}"
