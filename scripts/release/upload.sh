#!/usr/bin/env bash
# upload.sh
# Upload release artifacts from dist/ to a GitHub Release.
# Creates the release if it does not already exist (idempotent).
# Requires: gh CLI authenticated; GITHUB_TOKEN set by CI.
#
# Configuration via environment variable:
#   RELEASE_TAG   — git tag to upload to, e.g. v1.2.3  (required)
#                   If not a v* tag (e.g. triggered from a branch via
#                   workflow_dispatch), a tag is synthesized from git describe.

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
  local tag
  tag="$(resolve_tag "${RELEASE_TAG}")"
  log "Uploading release artifacts for ${tag}"
  require_gh
  collect_and_upload "${tag}"
  log "✅ Upload complete for ${tag}"
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

# resolve_tag echoes a v* tag, synthesizing one from git describe if the
# supplied value is not already a version tag.
function resolve_tag() {
  local -r raw="${1}"
  if [[ "${raw}" =~ ^v[0-9] ]]; then
    printf '%s' "${raw}"
    return
  fi
  log "  '${raw}' is not a version tag; synthesizing from git describe"
  local tag
  tag="$(synthesize_tag)"
  log "  Synthesized tag: ${tag}"
  printf '%s' "${tag}"
}

# synthesize_tag produces a tag from git describe --tags --dirty --always.
# Always includes a commit reference; appends -dirty if the tree is unclean.
function synthesize_tag() {
  git describe --tags --dirty --always
}

function require_gh() {
  if ! command -v gh >/dev/null 2>&1; then
    log "ERROR: gh CLI not found in PATH"
    exit 1
  fi
}

function collect_and_upload() {
  local -r tag="${1}"
  local files=()
  local artifact

  for artifact in dist/keel-* dist/SHA256SUMS; do
    [ -f "${artifact}" ] || continue
    files+=("${artifact}")
    log "  Queuing ${artifact}"
  done

  if [ "${#files[@]}" -eq 0 ]; then
    log "ERROR: no artifacts found in dist/"
    exit 1
  fi

  ensure_release "${tag}"
  # --clobber replaces existing assets of the same name (idempotent re-runs).
  gh release upload "${tag}" "${files[@]}" --clobber
}

# extract_changelog prints the body of the CHANGELOG.md section for version
# (e.g. "0.9.7"), or nothing if the section is absent.
function extract_changelog() {
  local -r version="${1}"
  awk '/^## \['"${version}"'\]/{found=1; next} found && /^## \[/{exit} found{print}' CHANGELOG.md 2>/dev/null
}

# ensure_release creates the GitHub Release for tag if it does not yet exist.
# Notes are drawn from CHANGELOG.md when a matching section exists; otherwise
# GitHub auto-generates notes from merged pull requests.
function ensure_release() {
  local -r tag="${1}"
  if gh release view "${tag}" >/dev/null 2>&1; then
    return
  fi
  log "  Release ${tag} not found; creating"
  local version notes tmp
  version="${tag#v}"
  notes="$(extract_changelog "${version}")"
  if [ -n "${notes}" ]; then
    tmp="$(mktemp /tmp/keel-release-XXXXXX.md)"
    printf '%s\n' "${notes}" >"${tmp}"
    gh release create "${tag}" --title "${tag}" --notes-file "${tmp}"
    rm -f "${tmp}"
  else
    gh release create "${tag}" --title "${tag}" --generate-notes
  fi
  log "  Release ${tag} created"
}

main "${@:-}"
