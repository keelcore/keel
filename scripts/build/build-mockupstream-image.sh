#!/usr/bin/env bash
# build-mockupstream-image.sh
# Builds keel-mockupstream:test, the mock upstream of the sidecar integration tests.
# The dev image compiles tests/mockupstream from this tree (keel as a library, tagged
# or not) into target/mockupstream, with the repository mounted read-only; the image
# then packages that binary alone (tests/mockupstream/Containerfile).
# Env: KEEL_IMAGE_TOOL (default docker) selects the builder: docker or container (Apple).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

declare -rx KEEL_IMAGE_TOOL="${KEEL_IMAGE_TOOL:-docker}"

readonly MOCK_IMAGE='keel-mockupstream:test'
readonly OUT_DIR='target/mockupstream'
readonly LOG_FILE='/tmp/keel_mockupstream_image.log'

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
  log '🔨 Compiling the mock upstream from this tree in the dev image...'
  rm -rf "${repo_root:?}/${OUT_DIR}"
  mkdir -p "${repo_root}/${OUT_DIR}"
  (cd "${repo_root}" && bash scripts/dev/run.sh env CGO_ENABLED=0 GOOS=linux go build -mod=vendor -trimpath \
    -ldflags='-s -w -buildid=' -o "${OUT_DIR}/mockupstream" ./tests/mockupstream)
  log "🐳 Building ${MOCK_IMAGE} with ${KEEL_IMAGE_TOOL}..."
  "${KEEL_IMAGE_TOOL}" build -t "${MOCK_IMAGE}" \
    -f "${repo_root}/tests/mockupstream/Containerfile" \
    "${repo_root}/${OUT_DIR}"
  log "✅ ${MOCK_IMAGE} built"
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
