#!/usr/bin/env bash
# ci.sh
# Handles versioning, SBOM generation, and FIPS symbol verification.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

function main() {
  exec 5>&1
  local -r log_file='/tmp/keel_release.log'
  validate_args "${@:-}"
  log "📦 Preparing release artifacts"
  generate_sbom
  verify_fips_compliance
  log "🚢 Release preparation complete"
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_release.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 0 ] && [ -z "${1:-}" ]; then
    log "Error: unexpected empty argument"
    exit 1
  fi
}

function generate_sbom() {
  log "🔍 Generating SBOM"
  run_syft
}

function run_syft() {
  if command -v syft >/dev/null 2>&1; then
    syft . -o json > 'dist/keel-sbom.json'
  else
    log "⚠️ Syft not found; skipping SBOM"
  fi
}

function verify_fips_compliance() {
  if [ "${FIPS_MODE:-}" = 'true' ]; then
    log "🛡 Checking FIPS BoringCrypto symbols"
    check_symbols
  fi
}

function check_symbols() {
  go tool nm 'dist/keel-linux-amd64' | \
    grep '_Cfunc__goboringcrypto_' || \
    handle_fips_failure
}

function handle_fips_failure() {
  log "❌ FIPS verification failed"
  exit 1
}

main "${@:-}"
