#!/usr/bin/env bash
# scripts/test/fuzz.sh
# Short fuzz pass: runs every native Go fuzz target (func FuzzXxx in *_test.go,
# outside vendor/) for FUZZTIME each (default 10s). A crash fails the run and
# Go writes the failing input under the package's testdata/fuzz/ for replay.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly FUZZ_TIME="${FUZZTIME:-10s}"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local targets
  targets="$(fuzz_targets)"
  [ -n "${targets}" ] || { log '❌ No fuzz targets found'; exit 1; }
  while IFS=' ' read -r pkg name; do
    run_target "${pkg}" "${name}"
  done <<< "${targets}"
  log '✅ All fuzz targets passed'
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_test_fuzz.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

# fuzz_targets prints "<package dir> <FuzzName>" for each fuzz target.
function fuzz_targets() {
  grep -rEo --include='*_test.go' --exclude-dir=vendor '^func Fuzz[A-Za-z0-9_]+' . \
    | sed -E 's#^(.*)/[^/]+_test\.go:func (Fuzz[A-Za-z0-9_]+)$#\1 \2#' \
    | sort -u
}

function run_target() {
  local -r pkg="${1}"
  local -r name="${2}"
  log "🧪 ${name} (${pkg}, ${FUZZ_TIME})"
  go test -run '^$' -fuzz "^${name}\$" -fuzztime "${FUZZ_TIME}" "${pkg}/"
}

main "${@:-}"
