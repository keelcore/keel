#!/usr/bin/env bash
# ci_example.sh
# Build the examples/myapp binary for integrity testing.

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
  log "🔨 Building examples/myapp binary..."
  mkdir -p dist
  go build -o dist/myapp ./examples/myapp
  log "✅ Build complete: dist/myapp"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_ci_example.log' >&5
}

function validate_args() { :; }

main "${@:-}"