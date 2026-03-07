#!/usr/bin/env bash
# upload.sh
# Upload release artifacts from dist/ to a GitHub Release.
# Requires: gh CLI authenticated; GITHUB_TOKEN set by CI.
#
# Configuration via environment variable:
#   RELEASE_TAG   — git tag to upload to, e.g. v1.2.3  (required)

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

function main() {
  exec 5>&1
  validate_args
  log "Uploading release artifacts for ${RELEASE_TAG}"
  require_gh
  collect_and_upload
  log "Upload complete"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_upload.log' >&5
}

function validate_args() {
  if [ -z "${RELEASE_TAG:-}" ]; then
    log "ERROR: RELEASE_TAG environment variable is required"
    log "  Example: RELEASE_TAG=v1.2.3 bash scripts/release/upload.sh"
    exit 1
  fi
}

function require_gh() {
  if ! command -v gh >/dev/null 2>&1; then
    log "ERROR: gh CLI not found in PATH"
    exit 1
  fi
}

function collect_and_upload() {
  local files=()
  local artifact

  for artifact in dist/keel-* dist/SHA256SUMS dist/keel-sbom.spdx.json; do
    [ -f "${artifact}" ] || continue
    files+=("${artifact}")
    log "  Queuing ${artifact}"
  done

  if [ "${#files[@]}" -eq 0 ]; then
    log "ERROR: no artifacts found in dist/"
    exit 1
  fi

  # --clobber replaces existing assets of the same name (idempotent re-runs).
  gh release upload "${RELEASE_TAG}" "${files[@]}" --clobber
}

main "${@:-}"