#!/usr/bin/env bash
# scripts/release/publish-github-release.sh
# Create (or update) the GitHub Release for TAG and attach every file in DIR
# (scripts/release/build-release-assets.sh). The notes carry the signed tag message, the
# CHANGELOG section for the version, the pins table, and how to verify the assets.
# Idempotent: a rerun updates the notes and replaces assets of the same name.
#
# Usage: publish-github-release.sh TAG DIR   (in the dev image: make publish-github-release TAG=vX.Y.Z)
# Env: GITHUB_TOKEN (contents: write), GITHUB_REPOSITORY (owner/repo).
# Project hook: publish-github-release.local.sh runs first, when present, with this script's arguments
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

readonly API='https://api.github.com'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  local -r tag="${1}" dir="${2}"
  local -r repo="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
  : "${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
  local notes id upload
  notes="$(release_notes "${tag}" "${dir}")"
  id="$(upsert_release "${repo}" "${tag}" "${notes}")"
  upload="https://uploads.github.com/repos/${repo}/releases/${id}/assets"
  local f
  for f in "${dir}"/*; do
    [ -f "${f}" ] || continue
    upload_asset "${repo}" "${id}" "${upload}" "${f}"
  done
  log "✅ https://github.com/${repo}/releases/tag/${tag}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/github_release.log' >&5
}

function validate_args() {
  if [ "${#}" -ne 2 ] || [[ ! "${1:-}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || [ ! -d "${2:-}" ]; then
    log '❌ Usage: publish-github-release.sh vX.Y.Z DIR'
    exit 1
  fi
}

function api() {
  curl -fsSL --connect-timeout 30 --speed-limit 1024 --speed-time 60 --retry 3 --retry-all-errors -H "Authorization: Bearer ${GITHUB_TOKEN}" \
    -H 'Accept: application/vnd.github+json' "$@"
}

# release_notes TAG DIR: Markdown notes for the release.
function release_notes() {
  local -r tag="${1}" dir="${2}"
  local -r version="${tag#v}"
  printf '%s\n\n' "$(git for-each-ref "refs/tags/${tag}" --format='%(contents:subject)')"
  git for-each-ref "refs/tags/${tag}" --format='%(contents:body)' | sed -e '/^$/N;/^\n$/D'
  printf '\n## Changes\n\n'
  changelog_section "${tag}" "${version}"
  if [ -f "${dir}/current_pins.md" ]; then
    printf '\n## Pins\n\n'
    sed -n '/^| Kind |/,$p' "${dir}/current_pins.md"
  fi
  printf '\n## Verify\n\n'
  printf '```sh\nsha256sum -c SHA256SUMS\n'
  printf 'cosign verify-blob --bundle FILE.bundle FILE \\\n'
  printf '  --certificate-oidc-issuer https://token.actions.githubusercontent.com \\\n'
  printf '  --certificate-identity-regexp '"'"'^https://github.com/%s/'"'"'\n```\n' "${GITHUB_REPOSITORY}"
  printf '\nEach `*.spdx.json` is the SBOM of the artifact it is named after; `<repository>-<image>.spdx.json`\n'
  printf '(e.g. `standards-dev.spdx.json`) describes a pinned tool image the release was built in.\n'
  provenance_note "${dir}"
}

# provenance_note DIR: the provenance files this release carries, by kind (none: nothing printed).
function provenance_note() {
  local -r dir="${1}"
  local -a kinds=()
  compgen -G "${dir}/*.npm-attestations.json" > /dev/null && kinds+=('`*.npm-attestations.json` (npm)')
  compgen -G "${dir}/*.pypi-provenance.json" > /dev/null && kinds+=('`*.pypi-provenance.json` (PyPI)')
  compgen -G "${dir}/*.github-attestations.json" > /dev/null && kinds+=('`*.github-attestations.json` (GitHub build attestations)')
  compgen -G "${dir}/*.zip.sumdb.txt" > /dev/null && kinds+=('`*.zip.sumdb.txt` (Go checksum database)')
  [ "${#kinds[@]}" -gt 0 ] || return 0
  local IFS=','
  printf 'Provenance as each registry published it: %s.\n' "${kinds[*]}" | sed 's/,/, /g'
}

# changelog_section TAG VERSION: TAG's CHANGELOG.md section for VERSION, else its [Unreleased].
function changelog_section() {
  local -r tag="${1}" version="${2}"
  local changelog section
  changelog="$(git show "${tag}:CHANGELOG.md" 2> /dev/null || true)"
  [ -n "${changelog}" ] || { printf 'No CHANGELOG.md.\n'; return 0; }
  section="$(awk -v h="## [${version}]" 'index($0, h) == 1 { on = 1; next } on && /^## \[/ { exit } on' <<< "${changelog}")"
  if [ -z "${section}" ]; then
    section="$(awk 'index($0, "## [Unreleased]") == 1 { on = 1; next } on && /^## \[/ { exit } on' <<< "${changelog}" \
      | new_since "$(git describe --tags --abbrev=0 "${tag}^" 2> /dev/null || true)")"
  fi
  printf '%s\n' "${section}"
}

# new_since PREV_TAG: the [Unreleased] section on stdin, keeping only the entries PREV_TAG's
# CHANGELOG.md does not have (an entry is its "- " line and the indented lines after it), under
# their "### " headings: what this release adds. Without PREV_TAG, every entry.
function new_since() {
  local -r prev="$(mktemp)"
  [ -z "${1}" ] || git show "${1}:CHANGELOG.md" > "${prev}" 2> /dev/null || true
  awk '
    FILENAME == ARGV[1] { seen[$0] = 1; next }
    /^### / { head = $0; next }
    /^- / { keep = !($0 in seen); if (keep && !(head in shown)) { print ""; print head; print ""; shown[head] = 1 } }
    /^- / || /^  / { if (keep) print }
  ' "${prev}" -
  rm -f "${prev}"
}

# upsert_release REPO TAG NOTES: the release id, created or with its notes updated.
function upsert_release() {
  local -r repo="${1}" tag="${2}" notes="${3}"
  local body id
  body="$(jq -n --arg t "${tag}" --arg b "${notes}" '{tag_name: $t, name: $t, body: $b}')"
  if id="$(api "${API}/repos/${repo}/releases/tags/${tag}" 2>/dev/null | jq -r '.id // empty')" && [ -n "${id}" ]; then
    api -X PATCH "${API}/repos/${repo}/releases/${id}" -d "${body}" > /dev/null
  else
    id="$(api -X POST "${API}/repos/${repo}/releases" -d "${body}" | jq -r '.id')"
  fi
  [[ "${id}" =~ ^[0-9]+$ ]] || { log "❌ could not create or update the release for ${tag}"; exit 1; }
  printf '%s' "${id}"
}

# upload_asset REPO ID UPLOAD_URL FILE: attach FILE, replacing an asset of the same name.
function upload_asset() {
  local -r repo="${1}" id="${2}" upload="${3}" file="${4}"
  local -r name="$(basename "${file}")"
  local existing
  existing="$(api "${API}/repos/${repo}/releases/${id}/assets?per_page=100" \
    | jq -r --arg n "${name}" '.[] | select(.name == $n) | .id')"
  [ -z "${existing}" ] || api -X DELETE "${API}/repos/${repo}/releases/assets/${existing}" > /dev/null
  api -X POST -H 'Content-Type: application/octet-stream' --data-binary "@${file}" \
    "${upload}?name=${name}" > /dev/null
  log "📎 ${name}"
}

main "${@:-}"
