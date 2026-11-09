#!/usr/bin/env bash
# scripts/release/wait-for-publish.sh
# Wait until a registry serves everything of the version this job just published. A publish returns
# before the registry has finished processing it, and the release job reads what the registries
# serve, so each publish job ends here. It gives up after WAIT_TIMEOUT seconds (default 1800).
#
# Usage: wait-for-publish.sh npm|pypi|go   (make wait-for-publish ARGS=...)
# Tag: release_tag (scripts/lib/release-tag.sh): INPUT_TAG, else GITHUB_REF_NAME on a tag push.
# Environment: WAIT_INTERVAL (default 15).
# Environment: WAIT_TIMEOUT (default 1800).
# Project hook: wait-for-publish.local.sh runs first, when present, with this script's arguments
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
# shellcheck source=../lib/release-tag.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/release-tag.sh"

declare -rx WAIT_TIMEOUT="${WAIT_TIMEOUT:-1800}"
declare -rx WAIT_INTERVAL="${WAIT_INTERVAL:-15}"
readonly INTERVAL="${WAIT_INTERVAL}"
readonly TIMEOUT="${WAIT_TIMEOUT}"
readonly PYPI_ACCEPT='Accept: application/vnd.pypi.integrity.v1+json'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  local tag
  tag="$(release_tag)"
  "wait_${1}" "${tag}"
  log "✅ ${1}: ${tag} is fully published"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/wait_published.log' >&5
}

function validate_args() {
  case "${1:-}" in
    npm | pypi | go) ;;
    *)
      log '❌ Usage: wait-for-publish.sh npm|pypi|go'
      exit 1
      ;;
  esac
}

# wait_for URL [CURL_ARG...]: the response body, once URL serves one.
function wait_for() {
  local -r url="${1}"
  shift
  local -r started="${SECONDS}"
  until curl -fsSL --connect-timeout 30 --speed-limit 1024 --speed-time 60 "$@" "${url}"; do
    if [ $(( SECONDS - started )) -ge "${TIMEOUT}" ]; then
      log "❌ gave up on ${url} after ${TIMEOUT}s" >&2
      exit 1
    fi
    log "⏳ waiting for ${url}" >&2
    sleep "${INTERVAL}"
  done
}

function wait_npm() {
  local -r tag="${1}"
  local name meta
  name="$(jq -r '.name' package.json)"
  meta="$(wait_for "https://registry.npmjs.org/${name/\//%2f}/${tag#v}")"
  wait_for "$(jq -r '.dist.tarball' <<< "${meta}")" > /dev/null
  wait_for "$(jq -r '.dist.attestations.url' <<< "${meta}")" > /dev/null
}

function wait_pypi() {
  local -r tag="${1}"
  local name meta path fn
  name="$(sed -nE 's/^name = "([^"]+)"$/\1/p' pyproject.toml | head -1)"
  # The packages only: the publish action leaves its attestation files beside them.
  for path in target/dist/*.whl target/dist/*.tar.gz; do
    [ -f "${path}" ] || continue
    fn="$(basename "${path}")"
    until meta="$(wait_for "https://pypi.org/pypi/${name}/${tag#v}/json")" \
      && jq -e --arg fn "${fn}" 'any(.urls[]; .filename == $fn)' <<< "${meta}" > /dev/null; do
      log "⏳ waiting for ${fn} in PyPI's listing"
      sleep "${INTERVAL}"
    done
    wait_for "$(jq -r --arg fn "${fn}" '.urls[] | select(.filename == $fn) | .url' <<< "${meta}")" > /dev/null
    wait_for "https://pypi.org/integrity/${name}/${tag#v}/${fn}/provenance" -H "${PYPI_ACCEPT}" > /dev/null
  done
}

function wait_go() {
  local -r tag="${1}"
  local module
  module="$(awk '$1 == "module" { print $2; exit }' go.mod)"
  wait_for "https://proxy.golang.org/${module}/@v/${tag}.zip" > /dev/null
  wait_for "https://sum.golang.org/lookup/${module}@${tag}" > /dev/null
}

main "${@:-}"
