#!/usr/bin/env bash
# scripts/test/native-integrity.sh
# Run the BATS integrity suite on a native macOS host with the bats and pebble the
# dev image cross-built (scripts/test/native-build.sh) first on PATH, against the
# keel binaries it built into dist/. The host installs nothing.
#
# Usage: native-integrity.sh DIR   (DIR: target/native/<os>-<arch>)

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
  local -r dir="$(cd "${1}" && pwd)"
  chmod +x "${dir}/bats/bin/bats" "${dir}/bin/"* dist/keel-*
  PATH="${dir}/bats/bin:${dir}/bin:${PATH}" bash scripts/test/bats-integrity.sh
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a "${TMPDIR:-/tmp}/keel_native_integrity.log" >&5
}

function validate_args() {
  if [ "${#}" -ne 1 ] || [ ! -f "${1:-}/bats/bin/bats" ]; then
    log '❌ Usage: native-integrity.sh target/native/<os>-<arch>   (built by make native-build)'
    exit 1
  fi
}

main "${@:-}"
