#!/usr/bin/env bash
# scripts/test/test-native-integrity.sh
# Run scripts/test/test-native-integrity.local.sh on a native host, with the tools the dev image
# cross-built (scripts/test/build-native.sh) first on PATH. The host installs nothing.
#
# Usage: test-native-integrity.sh DIR   (DIR: target/native/<os>-<arch>)

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# What the project runs natively.
readonly LOCAL_HOOK='scripts/test/test-native-integrity.local.sh'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local -r dir="$(cd "${1}" && pwd)"
  # Artifacts lose their executable bits in transit: bats runs its bin/ and libexec/ scripts.
  chmod +x "${dir}/bats/bin/"* "${dir}/bats/libexec/bats-core/"* "${dir}/bin/"* 2> /dev/null || true
  PATH="${dir}/bats/bin:${dir}/bin:${PATH}" bash "${LOCAL_HOOK}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a "${TMPDIR:-/tmp}/native_integrity.log" >&5
}

function validate_args() {
  if [ "${#}" -ne 1 ] || [ ! -f "${1:-}/bats/bin/bats" ]; then
    log '❌ Usage: test-native-integrity.sh target/native/<os>-<arch>   (built by make build-native)'
    exit 1
  fi
}

main "${@:-}"
