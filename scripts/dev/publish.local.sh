#!/usr/bin/env bash
# scripts/dev/publish.local.sh
# keel hook for the canonical scripts/dev/publish.sh, which runs it as
# `publish.local.sh MODE` after each mode. Project-owned, never canonical.
#
# Usage: publish.local.sh push|merge|ref|local   (make dev-image-publish-local WHAT=local)
#
# local (make dev-image-publish, the bootstrap from a dev workstation before the
# commit that pins the image is pushed): report each tool image's GHCR package —
# public, and connected to this repository so its Actions can write it (the
# dev-image workflow signs the image after the commit). GitHub has no API for
# either setting; the page to change it on is printed. Other modes: nothing.
#
# Env: GITHUB_TOKEN (exported by publish.sh local), else the gh credentials.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/dev-image.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/dev-image.sh"

declare -r LOG_FILE='/tmp/keel_dev_publish_local.log'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  [ "${1}" = 'local' ] || return 0
  GITHUB_TOKEN="${GITHUB_TOKEN:-$(gh auth token)}"
  local -r repo="$(origin_repo)"
  local cf name failed=0
  for cf in containers/*/Containerfile; do
    name="$(basename "$(dirname "${cf}")")"
    check_package "${repo}" "$(dev_repository . "${name}")" || failed=1
  done
  return "${failed}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a "${LOG_FILE}" >&5
}

function validate_args() {
  case "${1:-}" in
    push | merge | ref | local) ;;
    *)
      log '❌ Usage: publish.local.sh push|merge|ref|local'
      exit 1
      ;;
  esac
}

# origin_repo: OWNER/REPO of the origin remote.
function origin_repo() {
  git remote get-url origin | sed -E 's#^(git@[^:]+:|https://[^/]+/)##; s#\.git$##'
}

# check_package REPO REPOSITORY: report the GHCR package behind REPOSITORY
# (ghcr.io/OWNER/PACKAGE); fail when it is not public or REPO's Actions cannot write it.
function check_package() {
  local -r repo="${1}" repository="${2}"
  local -r owner="$(printf '%s' "${repository}" | cut -d/ -f2)" package="${repository##*/}"
  local -r settings="https://github.com/orgs/${owner}/packages/container/${package}/settings"
  local meta visibility linked ok=0
  meta="$(github_api "orgs/${owner}/packages/container/${package}")" \
    || { log "❌ ${repository}: package not found on GHCR"; return 1; }
  visibility="$(jq -r '.visibility' <<< "${meta}")"
  linked="$(jq -r '.repository.full_name // ""' <<< "${meta}")"
  if [ "${visibility}" = 'public' ]; then
    log "✅ ${repository}: public"
  else
    log "❌ ${repository}: ${visibility} — set Package visibility to Public: ${settings}"
    ok=1
  fi
  if [ "${linked}" = "${repo}" ]; then
    log "✅ ${repository}: connected to ${repo} (its Actions inherit write)"
  else
    log "❌ ${repository}: not connected to ${repo} — Connect Repository on the package page (its Actions then inherit write): https://github.com/orgs/${owner}/packages/container/package/${package}"
    ok=1
  fi
  return "${ok}"
}

function github_api() {
  curl -fsSL -H "Authorization: Bearer ${GITHUB_TOKEN}" -H 'Accept: application/vnd.github+json' \
    "https://api.github.com/${1}"
}

main "${@:-}"
