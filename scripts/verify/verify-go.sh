#!/usr/bin/env bash
# scripts/verify/verify-go.sh
# Builds and vets the Go module from the repository root.
# Project hook: verify-go.local.sh runs first, when present, with this script's arguments
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

function main() {
  exec 5>&1
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  validate_env
  build
  vet
}

function validate_env() {
  log 'Checking for go...'
  if ! command -v go > /dev/null 2>&1; then
    log '❌ go not found on PATH'
    exit 1
  fi
  if [ ! -f 'go.mod' ]; then
    log '❌ go.mod not found; run from the repository root'
    exit 1
  fi
  log '✅ Environment valid'
}

function build() {
  log 'Building Go module...'
  go build ./...
  log '✅ Build passed'
}

function vet() {
  log 'Vetting Go module...'
  go vet ./...
  log '✅ Vet passed'
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/go-verify.log' >&5
}

main "${@:-}"
