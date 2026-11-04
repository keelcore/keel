#!/usr/bin/env bash
# scripts/dev/sign-image.sh
# Sign a published image by digest, keyless (Sigstore, GitHub OIDC). Runs in the
# dev image (cosign is baked in), called by scripts/dev/publish.sh merge.
#
# Usage: sign-image.sh REPOSITORY@sha256:DIGEST   (no-op when this repository's workflow
# already signed it)
# Env: GITHUB_TOKEN, GITHUB_ACTOR (registry login); ACTIONS_ID_TOKEN_REQUEST_URL and
# ACTIONS_ID_TOKEN_REQUEST_TOKEN (the job's OIDC token for the signing certificate).

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
  local -r ref="${1}"
  if cosign verify --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
    --certificate-identity-regexp "^https://github.com/${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}/" \
    "${ref}" > /dev/null 2>&1; then
    log "✅ ${ref} is already signed"
    return 0
  fi
  printf '%s' "${GITHUB_TOKEN:?GITHUB_TOKEN is required}" \
    | cosign login "${ref%%/*}" -u "${GITHUB_ACTOR:?GITHUB_ACTOR is required}" --password-stdin
  cosign sign --yes "${ref}"
  log "✅ signed ${ref}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keelcore_sign_image.log' >&5
}

function validate_args() {
  if [ "${#}" -ne 1 ] || [[ ! "${1:-}" =~ @sha256:[0-9a-f]{64}$ ]]; then
    log '❌ Usage: sign-image.sh REPOSITORY@sha256:DIGEST'
    exit 1
  fi
}

main "${@:-}"
