#!/usr/bin/env bash
# sbom.sh
# Generate a machine-readable SBOM (SPDX JSON) for the repository using syft.
# Output: dist/keel-sbom.spdx.json
#
# Requires: syft in PATH.
# Install: https://github.com/anchore/syft#installation

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly SBOM_OUTPUT='dist/keel-sbom.spdx.json'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  prepare_dist
  log "Generating SBOM"
  require_syft
  generate_sbom
  log "SBOM written to ${SBOM_OUTPUT}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_sbom.log' >&5
}

function validate_args() { :; }

function prepare_dist() { mkdir -p 'dist'; }

function require_syft() {
  if ! command -v syft >/dev/null 2>&1; then
    log "ERROR: syft not found in PATH"
    log "  Install: curl -sSfL https://raw.githubusercontent.com/anchore/syft/main/install.sh | sh -s -- -b /usr/local/bin"
    exit 1
  fi
}

function generate_sbom() {
  syft . \
    --output "spdx-json=${SBOM_OUTPUT}" \
    --quiet
}

main "${@:-}"
