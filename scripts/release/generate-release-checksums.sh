#!/usr/bin/env bash
# generate-release-checksums.sh
# Generate or verify SHA256SUMS for release artifacts in dist/.
#
# Usage:
#   generate-release-checksums.sh            Generate dist/SHA256SUMS
#   generate-release-checksums.sh --verify   Verify existing dist/SHA256SUMS
# Project hook: generate-release-checksums.local.sh runs first, when present, with this script's arguments
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

# shellcheck source=../lib/tool-config.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/tool-config.sh"

readonly SUMS_FILE='dist/SHA256SUMS'

function main() {
  exec 5>&1
  if [ "${1:-}" = '--verify' ]; then
    verify_checksums
  else
    validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
    prepare_dist
    generate_checksums
  fi
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/release_checksums.log' >&5
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

  while IFS= read -r artifact; do
    sha256_generate "${artifact}" >> "${SUMS_FILE}"
    log "  ${artifact}"
    found=1
  done < <(release_files)

  if [ "${found}" -eq 0 ]; then
    log "ERROR: no artifacts found in dist/"
    exit 1
  fi

  # dist/dev-image.txt: every tool image that built the release, the dev image first.
  if [ -n "$(release_tool_images)" ]; then
    release_tool_images > dist/dev-image.txt
    sha256_generate dist/dev-image.txt >> "${SUMS_FILE}"
    log '  dist/dev-image.txt'
  fi

  log "Checksums written to ${SUMS_FILE}"
}

function verify_checksums() {
  if [ ! -f "${SUMS_FILE}" ]; then
    log "ERROR: ${SUMS_FILE} not found — run without --verify to generate"
    exit 1
  fi
  log "Verifying ${SUMS_FILE}"
  sha256_check "${SUMS_FILE}"
  log "Checksum verification passed"
}

main "${@:-}"
