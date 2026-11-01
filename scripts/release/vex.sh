#!/usr/bin/env bash
# scripts/release/vex.sh
# Generates the release's OpenVEX document from govulncheck's reachability
# analysis: every vulnerability govulncheck finds in a required module but
# cannot reach from keel's code is stated not_affected /
# vulnerable_code_not_present. Generated per release from the code being
# shipped, never committed. docker.sh attests it to every image index.
#
# Usage: scripts/release/vex.sh [--out-dir DIR]   (default: dist)
# Requires: govulncheck on PATH (make setup-govulncheck)

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

declare OUT_DIR='dist'

function main() {
  exec 5>&1
  if [ "${1:-}" = '--out-dir' ]; then
    OUT_DIR="${2}"
    shift 2
  fi
  validate_args "${@:-}"
  mkdir -p "${OUT_DIR}"
  log "Generating OpenVEX → ${OUT_DIR}/keel.openvex.json"
  govulncheck -format openvex ./... > "${OUT_DIR}/keel.openvex.json"
  log "✅ OpenVEX: $(jq '.statements | length' "${OUT_DIR}/keel.openvex.json") statement(s)"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_release_vex.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
