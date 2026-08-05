#!/usr/bin/env bash
# scripts/release/sbom.sh
# Generates a Software Bill of Materials (SBOM) in SPDX format for this release.
# Outputs sbom.spdx.json in the current directory.
#
# Requires: syft on PATH (https://github.com/anchore/syft)
# Install in CI: curl -sSfL https://raw.githubusercontent.com/anchore/syft/main/install.sh | sh

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly OUTPUT_FILE='sbom.spdx.json'

function main() {
  exec 5>&1
  validate_env
  generate_sbom
  log_summary
}

function validate_env() {
  log 'Validating environment...'
  if ! command -v syft > /dev/null 2>&1; then
    log '❌ syft not found on PATH'
    log '   Install: curl -sSfL https://raw.githubusercontent.com/anchore/syft/main/install.sh | sh -s -- -b /usr/local/bin'
    exit 1
  fi
  log '✅ Environment valid'
}

function generate_sbom() {
  log "Generating SBOM → ${OUTPUT_FILE}..."
  syft scan dir:. \
    --output spdx-json="${OUTPUT_FILE}" \
    --exclude './.git' \
    --exclude '**/node_modules'
  log "✅ SBOM generated: ${OUTPUT_FILE}"
}

function log_summary() {
  local component_count
  component_count="$(jq '.packages | length' "${OUTPUT_FILE}" 2>/dev/null || echo 'unknown')"
  log "   Components catalogued: ${component_count}"
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/sbom.log' >&5
}

main "${@:-}"
