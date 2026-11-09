#!/usr/bin/env bash
# scripts/release/verify-release-blobs.sh
# The release's last step: check every asset in DIR against SHA256SUMS and every
# cosign bundle against this repository's release workflow identity. Fails if a
# file has no signature.
#
# Usage: verify-release-blobs.sh DIR   (in the dev image: make verify-release-blobs)
# Env: GITHUB_REPOSITORY (owner/repo: the signing identity).
# Environment: GITHUB_REPOSITORY (default empty).
# Project hook: verify-release-blobs.local.sh runs first, when present, with this script's arguments
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

declare -rx GITHUB_REPOSITORY="${GITHUB_REPOSITORY:-}"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  local -r dir="${1}"
  local -r identity="^https://github.com/${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}/"
  (cd "${dir}" && sha256sum -c --quiet SHA256SUMS)
  log '✅ SHA256SUMS'
  local f n=0
  for f in "${dir}"/*; do
    [ -f "${f}" ] || continue
    case "${f}" in *.bundle | *.sigstore.json) continue ;; esac
    [ -f "${f}.bundle" ] || { log "❌ no signature for $(basename "${f}")"; exit 1; }
    cosign verify-blob --bundle "${f}.bundle" \
      --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
      --certificate-identity-regexp "${identity}" "${f}" > /dev/null
    n=$(( n + 1 ))
  done
  log "✅ ${n} signatures verified (${identity})"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/verify_blobs.log' >&5
}

function validate_args() {
  if [ "${#}" -ne 1 ] || [ ! -f "${1:-}/SHA256SUMS" ]; then
    log '❌ Usage: verify-release-blobs.sh DIR   (DIR holds SHA256SUMS)'
    exit 1
  fi
}

main "${@:-}"
