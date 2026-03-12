#!/usr/bin/env bash
# kind-load.sh
# Builds keel:test Docker image and loads it into the keel-ci kind cluster.
# Linux: uses pre-built dist/keel-min (from CI artifact).
# macOS: cross-compiles GOOS=linux GOARCH=arm64 from source.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly CLUSTER_NAME='keel-ci'
readonly TINY_TAGS='no_acme,no_authz,no_fips,no_h2,no_otel,no_owasp,no_prom,no_remotelog,no_authn,no_h3,no_sidecar,no_statsd'
readonly LOG_FILE='/tmp/keel_kind_load.log'
REPO_ROOT=''

function main() {
  exec 5>&1
  validate_args "${@:-}"
  REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  log '🐳 Building and loading keel:test into kind cluster...'
  build_image
  load_image
  log '✅ keel:test loaded into kind cluster'
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

function build_image() {
  local os tmp
  os="$(uname -s)"
  tmp="$(mktemp -d)"
  if [[ "${os}" == 'Darwin' ]]; then
    log '⚙️  Cross-compiling for linux/arm64 (macOS host)...'
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
      go build -trimpath -tags "${TINY_TAGS}" \
      -ldflags='-s -w -buildid=' \
      -o "${tmp}/keel" "${REPO_ROOT}/cmd/keel"
  else
    log '📦 Staging pre-built dist/keel-min...'
    cp "${REPO_ROOT}/dist/keel-min" "${tmp}/keel"
  fi
  log '🏗️  Building keel:test Docker image...'
  docker build -t keel:test \
    --build-arg FLAVOR=min \
    -f "${REPO_ROOT}/scripts/deploy/docker/Dockerfile.release" \
    "${tmp}"
  rm -rf "${tmp}"
  log '✅ keel:test image built'
}

function load_image() {
  log "📤 Loading keel:test into kind cluster '${CLUSTER_NAME}'..."
  kind load docker-image keel:test --name "${CLUSTER_NAME}"
  log '✅ Image loaded'
}

main "${@:-}"
