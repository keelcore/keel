#!/usr/bin/env bash
# certs-image.sh
# Builds keel-certs:latest from scripts/deploy/docker/Dockerfile.certs: a FROM
# scratch image holding the full Mozilla CA bundle. keel's runtime images copy
# an explicit allowlist of roots out of it, so it must exist before they build.
# KEEL_IMAGE_TOOL selects the builder: docker (default) or container (Apple).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly CERTS_IMAGE='keel-certs:latest'
readonly LOG_FILE='/tmp/keel_certs_image.log'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local repo_root tool
  repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  tool="${KEEL_IMAGE_TOOL:-docker}"
  case "${tool}" in
    docker | container) ;;
    *)
      log "❌ KEEL_IMAGE_TOOL must be docker or container (got: ${tool})"
      exit 1
      ;;
  esac
  log "🔐 Building ${CERTS_IMAGE} with ${tool}..."
  "${tool}" build -t "${CERTS_IMAGE}" \
    -f "${repo_root}/scripts/deploy/docker/Dockerfile.certs" \
    "${repo_root}/scripts/deploy/docker"
  log "✅ ${CERTS_IMAGE} built"
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a "${LOG_FILE}" >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
