#!/usr/bin/env bash
# build-certs-image.sh
# Puts the certs tool image on this machine as keel-certs:latest: a FROM scratch image holding the full
# Mozilla CA bundle (scripts/deploy/docker/Dockerfile.certs). keel's runtime images copy an explicit
# allowlist of roots out of it, so it must exist before they build. It is the tool image listed in
# config/tool-images.local.txt, named by its build context (Dockerfile.certs.image.ref): pulled when
# published, else built locally from that context (scripts/dev/run.sh). Nothing else builds it.
# Env: KEEL_IMAGE_TOOL (default docker) selects the image store it is tagged in: docker or container (Apple).
# KEELCORE_RUN_SH (default scripts/dev/run.sh, run from the repository root; a seam, so tests stub it).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

declare -rx KEEL_IMAGE_TOOL="${KEEL_IMAGE_TOOL:-docker}"
declare -rx KEELCORE_RUN_SH="${KEELCORE_RUN_SH:-scripts/dev/run.sh}"

readonly CERTS_IMAGE='keel-certs:latest'
readonly LOG_FILE='/tmp/keel_certs_image.log'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local repo_root
  repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  case "${KEEL_IMAGE_TOOL}" in
    docker | container) ;;
    *)
      log "❌ KEEL_IMAGE_TOOL must be docker or container (got: ${KEEL_IMAGE_TOOL})"
      exit 1
      ;;
  esac
  local ref
  log '🔐 Fetching the certs tool image...'
  (cd "${repo_root}" && bash "${KEELCORE_RUN_SH}" --image certs --fetch-image)
  ref="$(cd "${repo_root}" && bash "${KEELCORE_RUN_SH}" --image certs --image-ref)"
  "${KEEL_IMAGE_TOOL}" image tag "${ref}" "${CERTS_IMAGE}"
  log "✅ ${CERTS_IMAGE} is ${ref}"
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
