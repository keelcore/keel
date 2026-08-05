#!/usr/bin/env bash
# scripts/release/go-publish.sh
# Validates the Go module and notifies the Go module proxy to index the new tag.
# Go modules are published by tagging a commit on GitHub; pkg.go.dev crawls the proxy.
# This script verifies the build and triggers proxy indexing for fast availability.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly MODULE='github.com/keelcore/standards'

function main() {
  exec 5>&1
  validate_env
  local tag
  tag="$(resolve_tag)"
  verify_build
  notify_proxy "${tag}"
}

function validate_env() {
  log 'Validating environment...'
  if [ ! -f 'go.mod' ]; then
    log '❌ go.mod not found; run from the repository root'
    exit 1
  fi
  log '✅ Environment valid'
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

function verify_build() {
  log 'Verifying Go module builds...'
  go build ./...
  log '✅ Go module verified'
}

function notify_proxy() {
  local -r tag="${1}"
  local -r version="${tag}"
  log "Notifying Go module proxy for ${MODULE}@${version}..."
  curl --silent --fail --show-error \
    "https://sum.golang.org/lookup/${MODULE}@${version}" > /dev/null
  curl --silent --fail --show-error \
    "https://proxy.golang.org/${MODULE}/@v/${version}.info" > /dev/null
  log "✅ Go proxy notified for ${MODULE}@${version}"
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/go-publish.log' >&5
}

main "${@:-}"
