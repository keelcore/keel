#!/usr/bin/env bash
# scripts/test/test-native.sh
# Run, on a native macOS or Windows host, the Go test binaries the dev image
# cross-built (scripts/test/build-native.sh) — no toolchain on the host. Each
# binary runs from its package directory, as `go test` runs it.
#
# Usage: test-native.sh DIR   (DIR: target/native/<os>-<arch>)
# Exits 1 if any package fails; prints "N of N passed, M skipped" per test.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local -r dir="${1}" root="${PWD}"
  local pkg bin logf failed=0 pass=0 fail=0 skip=0
  mkdir -p "${dir}/logs"
  # Artifacts lose their executable bit in transit.
  chmod +x "${dir}"/tests/* 2> /dev/null || true
  while IFS=$'\t' read -r pkg bin; do
    [ -n "${pkg}" ] || continue
    logf="${root}/${dir}/logs/$(basename "${bin}").log"
    if (cd "${root}/${pkg}" && "${root}/${dir}/${bin}" -test.v -test.count=1 -test.timeout=10m) > "${logf}" 2>&1; then
      log "✅ ${pkg}"
    else
      log "❌ ${pkg} (log: ${logf})"
      tail -40 "${logf}" >&5
      failed=1
    fi
    pass=$(( pass + $(grep -cE '^\s*--- PASS' "${logf}" || true) ))
    fail=$(( fail + $(grep -cE '^\s*--- FAIL' "${logf}" || true) ))
    skip=$(( skip + $(grep -cE '^\s*--- SKIP' "${logf}" || true) ))
  done < "${dir}/tests.tsv"
  log "Tests: $(( pass )) of $(( pass + fail )) passed, ${skip} skipped"
  return "${failed}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a "${TMPDIR:-/tmp}/native_run.log" >&5
}

function validate_args() {
  if [ "${#}" -ne 1 ] || [ ! -f "${1:-}/tests.tsv" ]; then
    log '❌ Usage: test-native.sh target/native/<os>-<arch>   (built by make build-native)'
    exit 1
  fi
}

main "${@:-}"
