#!/usr/bin/env bash
# checksums.sh
# Generate or verify SHA256SUMS for release artifacts in dist/.
#
# Usage:
#   checksums.sh            Generate dist/SHA256SUMS
#   checksums.sh --verify   Verify existing dist/SHA256SUMS

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly SUMS_FILE='dist/SHA256SUMS'

function main() {
  exec 5>&1
  if [ "${1:-}" = '--verify' ]; then
    verify_checksums
  else
    validate_args "${@:-}"
    prepare_dist
    generate_checksums
  fi
}

function log() {
  printf '%s\n' "${1:-}" >&5
}

function validate_args() { :; }

function prepare_dist() { mkdir -p 'dist'; }

# sha256sum is GNU coreutils (Linux); shasum -a 256 is macOS / BSD.
function sha256_generate() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$@"
  else
    shasum -a 256 "$@"
  fi
}

function sha256_check() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum --check "$@"
  else
    shasum -a 256 --check "$@"
  fi
}

function generate_checksums() {
  log "Generating ${SUMS_FILE}"
  local artifact
  local found=0

  # Start with an empty file.
  : > "${SUMS_FILE}"

  for artifact in dist/keel-*; do
    # Skip existing signature bundles and the sums file itself.
    [[ "${artifact}" == *.bundle ]] && continue
    [[ "${artifact}" == *.sig ]]    && continue
    [ -f "${artifact}" ]            || continue

    sha256_generate "${artifact}" >> "${SUMS_FILE}"
    log "  ${artifact}"
    found=1
  done

  if [ "${found}" -eq 0 ]; then
    printf 'ERROR: no artifacts found in dist/\n' >&2
    exit 1
  fi

  log "Checksums written to ${SUMS_FILE}"
}

function verify_checksums() {
  if [ ! -f "${SUMS_FILE}" ]; then
    printf 'ERROR: %s not found — run without --verify to generate\n' "${SUMS_FILE}" >&2
    exit 1
  fi
  log "Verifying ${SUMS_FILE}"
  sha256_check "${SUMS_FILE}"
  log "Checksum verification passed"
}

main "${@:-}"