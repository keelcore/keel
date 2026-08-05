#!/usr/bin/env bash
# scripts/release/npm-publish.sh
# Publishes @keelcore/standards to the npm registry.
# Version is derived from the git tag (GITHUB_REF_NAME or INPUT_TAG).
# Uses OIDC trusted publishing — no token required in CI.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

function main() {
  exec 5>&1
  validate_env
  local tag version
  tag="$(resolve_tag)"
  version="${tag#v}"
  log "Publishing @keelcore/standards@${version}..."
  npm version "${version}" --no-git-tag-version
  npm publish --access public
  log '✅ Published to npm'
}

function resolve_tag() {
  if [ -n "${GITHUB_REF_NAME:-}" ]; then
    printf '%s' "${GITHUB_REF_NAME}"
  elif [ -n "${INPUT_TAG:-}" ]; then
    printf '%s' "${INPUT_TAG}"
  else
    log '❌ Cannot resolve tag: GITHUB_REF_NAME and INPUT_TAG are both unset'
    exit 1
  fi
}

function validate_env() {
  log 'Validating environment...'
  if [ ! -f 'package.json' ]; then
    log '❌ package.json not found; run from the repository root'
    exit 1
  fi
  log '✅ Environment valid'
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/npm-publish.log' >&5
}

main "${@:-}"
