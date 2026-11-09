#!/usr/bin/env bash
# generate-release-metadata.sh
# Generate the release metadata concurrently (scripts/lib/parallel.sh): one SBOM per release binary
# (the project, its version, the Go modules compiled into it) and the OpenVEX document from govulncheck
# reachability. Runs in the dev image (make generate-release-metadata).
#
# Required environment variables:
#   TAG — the release tag
# Environment: TAG (default empty).
# Project hook: generate-release-metadata.local.sh runs first, when present, with this script's arguments
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

# shellcheck source=../lib/parallel.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/parallel.sh"
# shellcheck source=../lib/tool-config.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/tool-config.sh"

declare -rx TAG="${TAG:-}"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  run_parallel generate sbom vex
  log '✅ Release metadata generated'
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/release_metadata.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
  : "${TAG:?TAG is required}"
}

# generate KIND: one kind of metadata, as a run_parallel job.
function generate() {
  case "${1}" in
    sbom)
      bash scripts/release/generate-sbom.sh --out-dir dist --name "$(project_name)" --version "${TAG}" \
        --license Apache-2.0 "dist/$(project_name)"-*-linux-*
      ;;
    vex) bash scripts/release/generate-vex.sh --out-dir dist ;;
  esac
}

main "${@:-}"
