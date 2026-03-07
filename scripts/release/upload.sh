#!/usr/bin/env bash
# upload.sh
# Upload release artifacts from dist/ to a GitHub Release.
# Requires: gh CLI authenticated; GITHUB_TOKEN set by CI.
#
# Usage: upload.sh <tag>   e.g.  upload.sh v1.2.3

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
  local -r tag="${1}"
  log "Uploading release artifacts for ${tag}"
  require_gh
  collect_and_upload "${tag}"
  log "Upload complete"
}

function log() {
  printf '%s\n' "${1:-}" >&5
}

function validate_args() {
  if [ "${#}" -lt 1 ] || [ -z "${1:-}" ]; then
    printf 'Usage: %s <tag>\n' "${0}" >&2
    exit 1
  fi
}

function require_gh() {
  if ! command -v gh >/dev/null 2>&1; then
    printf 'ERROR: gh CLI not found in PATH\n' >&2
    exit 1
  fi
}

function collect_and_upload() {
  local -r tag="${1}"
  local files=()
  local artifact

  for artifact in dist/keel-* dist/SHA256SUMS dist/keel-sbom.spdx.json; do
    [ -f "${artifact}" ] || continue
    files+=("${artifact}")
    log "  Queuing ${artifact}"
  done

  if [ "${#files[@]}" -eq 0 ]; then
    printf 'ERROR: no artifacts found in dist/\n' >&2
    exit 1
  fi

  # --clobber replaces existing assets of the same name (idempotent re-runs).
  gh release upload "${tag}" "${files[@]}" --clobber
}

main "${@:-}"