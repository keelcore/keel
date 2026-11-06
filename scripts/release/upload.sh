#!/usr/bin/env bash
# upload.sh
# Upload release artifacts from dist/ to a GitHub Release.
# Creates the release as a draft if it does not already exist (idempotent). With --publish (the
# release workflow's last step), attaches the per-image SBOMs docker.sh wrote and publishes the
# draft: the repository has immutable releases, so every asset is attached before publishing.
#
# Usage: upload.sh [--publish]
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

# shellcheck source=../lib/tool-config.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/tool-config.sh"

# shellcheck source=../lib/parallel.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/parallel.sh"

PROJECT="$(project_name)"
readonly PROJECT

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local tag
  tag="$(resolve_tag "${RELEASE_TAG}")"
  require_gh
  if [ "${1:-}" = '--publish' ]; then
    publish_release "${tag}"
    return 0
  fi
  log "Uploading release artifacts for ${tag}"
  collect_and_upload "${tag}"
  log "✅ Upload complete for ${tag}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/upload.log' >&5
}

function validate_args() {
  if [ -z "${RELEASE_TAG:-}" ]; then
    log "ERROR: RELEASE_TAG environment variable is required"
    log "  Example: RELEASE_TAG=v1.2.3 bash scripts/release/upload.sh"
    exit 1
  fi
  case "${1:-}" in
    '' | --publish) ;;
    *)
      log "ERROR: unexpected argument: ${1}"
      exit 1
      ;;
  esac
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

# stage_scorecard_assets names release assets the way OpenSSF Scorecard
# (Signed-Releases) detects them: each cosign bundle also as
# <artifact>.sigstore.json, and the SLSA provenance DSSE envelope from
# PROVENANCE_BUNDLE (actions/attest-build-provenance bundle-path, or the
# directory holding it) as <project>.intoto.jsonl.
function stage_scorecard_assets() {
  local bundle
  for bundle in dist/*.bundle; do
    [ -f "${bundle}" ] || continue
    cp -p "${bundle}" "${bundle%.bundle}.sigstore.json"
  done
  if [ -n "${PROVENANCE_BUNDLE:-}" ]; then
    local provenance="${PROVENANCE_BUNDLE}"
    if [ -d "${provenance}" ]; then
      provenance="$(find "${provenance}" -type f | head -n 1)"
    fi
    jq -ce '.dsseEnvelope' "${provenance}" > "dist/${PROJECT}.intoto.jsonl"
    log "  Staged dist/${PROJECT}.intoto.jsonl"
  fi
}

function collect_and_upload() {
  local -r tag="${1}"
  local files=()
  local artifact

  stage_scorecard_assets
  for artifact in "dist/${PROJECT}"-* dist/SHA256SUMS dist/SHA256SUMS.bundle dist/SHA256SUMS.sigstore.json \
    "dist/${PROJECT}.openvex.json" "dist/${PROJECT}.intoto.jsonl"; do
    [ -f "${artifact}" ] || continue
    files+=("${artifact}")
    log "  Queuing ${artifact}"
  done

  if [ "${#files[@]}" -eq 0 ]; then
    log "ERROR: no artifacts found in dist/"
    exit 1
  fi

  ensure_release "${tag}"
  run_parallel upload_asset "${files[@]}" -- "${tag}"
}

# upload_asset FILE TAG: one asset, as a run_parallel job. --clobber replaces an existing asset of
# the same name (idempotent re-runs).
function upload_asset() {
  gh release upload "${2}" "${1}" --clobber
}

# publish_release TAG: attach the per-image SBOMs (variant × arch, all required), then publish the
# draft; publishing locks the release (immutable releases).
function publish_release() {
  local -r tag="${1}"
  local -a files=()
  local flavor arch
  for flavor in $(release_variants | cut -f1); do
    for arch in amd64 arm64; do
      [ -f "dist/sbom-${flavor}-${arch}.spdx.json" ] \
        || { log "ERROR: image SBOM missing: dist/sbom-${flavor}-${arch}.spdx.json"; exit 1; }
      files+=("dist/sbom-${flavor}-${arch}.spdx.json")
    done
  done
  log "Attaching ${#files[@]} image SBOMs to ${tag}"
  run_parallel upload_asset "${files[@]}" -- "${tag}"
  gh release edit "${tag}" --draft=false --latest
  log "✅ Release ${tag} published"
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
  local version notes overrides tmp
  version="${tag#v}"
  notes="$(extract_changelog "${version}")"
  overrides="$(pin_overrides "${tag}")"
  if [ -n "${overrides}" ]; then
    notes="$(printf '%s\n\n## Pin overrides\n\nThis release was authorized with stale pins (create-release --allow-stale-pins):\n\n%s\n' \
      "${notes}" "$(printf '%s\n' "${overrides}" | sed 's/^/- /')")"
  fi
  if [ -n "${notes}" ]; then
    tmp="$(mktemp /tmp/release-XXXXXX.md)"
    printf '%s\n' "${notes}" >"${tmp}"
    gh release create "${tag}" --title "${tag}" --draft --notes-file "${tmp}"
    rm -f "${tmp}"
  else
    gh release create "${tag}" --title "${tag}" --draft --generate-notes
  fi
  log "  Release ${tag} created (draft)"
}

# pin_overrides TAG: the Pins-Override* and Pins-Stale trailers create-release
# wrote into the signed tag message (empty when the release had no override).
function pin_overrides() {
  git tag -l --format='%(contents)' "${1}" | grep -E '^Pins-(Override|Override-By|Override-At|Stale):' || true
}

main "${@:-}"
