#!/usr/bin/env bash
# gen-schema.sh
# Regenerate pkg/config/schema.yaml from the config.Config reflection walk.
#
# Usage:
#   gen-schema.sh

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly SCHEMA_PATH='pkg/config/schema.yaml'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  ensure_repo_root
  generate
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_gen_schema.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    printf 'Usage: gen-schema.sh\n' >&2
    exit 1
  fi
}

function ensure_repo_root() {
  local root
  root="$(git rev-parse --show-toplevel)"
  cd "${root}"
}

function generate() {
  log "Generating ${SCHEMA_PATH}"
  go run ./cmd/config-schema/ > "${SCHEMA_PATH}"
  rm -f config-schema
  log "✅ Schema written to ${SCHEMA_PATH}"
}

main "${@:-}"