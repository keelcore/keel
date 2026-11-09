#!/usr/bin/env bash
# scripts/test/test-fuzz.sh
# Run every Fuzz* target for FUZZTIME (default 10s). A repo with no fuzz target has nothing to do.
# Environment: FUZZTIME (default 10s).
# Project hook: test-fuzz.local.sh runs first, when present, with this script's arguments
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

# shellcheck source=../lib/paths.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"

declare -rx FUZZTIME="${FUZZTIME:-10s}"
readonly FUZZ_TIME="${FUZZTIME}"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  local targets pkg name
  targets="$(fuzz_targets)"
  if [ -z "${targets}" ]; then
    log 'ℹ️  No fuzz targets; nothing to run'
    return 0
  fi
  while IFS=' ' read -r pkg name; do
    log "🧪 ${name} (${pkg}, ${FUZZ_TIME})"
    go test -run '^$' -fuzz "^${name}\$" -fuzztime "${FUZZ_TIME}" "./${pkg}/"
  done <<< "${targets}"
  log '✅ All fuzz targets passed'
}

# fuzz_targets prints "<package dir> <FuzzName>" for each fuzz target in repo-owned test files.
function fuzz_targets() {
  local file
  while IFS= read -r file; do
    grep -Eo '^func Fuzz[A-Za-z0-9_]+' "${file}" | sed -E "s#^func (.*)\$#$(dirname "${file}") \\1#" || true
  done < <(source_files '*_test.go') | sort -u
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/test_fuzz.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
