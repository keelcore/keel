#!/usr/bin/env bash
# setup-ghcr.sh
# One-time setup: make GHCR packages publicly visible after the first release push.
#
# Run this from your laptop once after the first `v*` tag triggers release.yml.
# Requires: gh CLI authenticated as an org owner or package admin on keelcore.
#
# Usage:
#   bash scripts/release/setup-ghcr.sh
#
# Packages configured:
#   ghcr.io/keelcore/keel           (container image)
#   ghcr.io/keelcore/charts/keel    (Helm OCI artifact)

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly ORG='keelcore'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  require_gh
  require_auth
  make_public 'keel'         'container image'
  make_public 'charts%2Fkeel' 'Helm OCI chart'
  log "Done. Both packages are now public on ghcr.io/${ORG}."
  log "Verify at: https://github.com/orgs/${ORG}/packages"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" >&5
}

function validate_args() { :; }

function require_gh() {
  if ! command -v gh >/dev/null 2>&1; then
    log "ERROR: gh CLI not found in PATH"
    log "  Install: https://cli.github.com"
    exit 1
  fi
}

function require_auth() {
  if ! gh auth status >/dev/null 2>&1; then
    log "ERROR: not authenticated — run: gh auth login"
    exit 1
  fi
  log "Authenticated as: $(gh api user --jq '.login')"
}

function make_public() {
  local -r pkg="${1}"
  local -r label="${2}"
  log "Setting ${label} (${pkg}) to public..."
  gh api \
    --method PATCH \
    "/orgs/${ORG}/packages/container/${pkg}" \
    --field visibility=public
  log "  OK: ghcr.io/${ORG}/${pkg//%2F//} is now public"
}

main "${@:-}"
