#!/usr/bin/env bash
# build-min.sh
# Minimalist Build: Smallest standalone footprint for BYOS users.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# Output directory; scripts/release/build-binaries.sh gives each architecture its own.
readonly DIST="${KEEL_DIST_DIR:-dist}"

readonly REQUIRED_GO_VERSION="go1.2"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  verify_toolchain
  log "Starting Minimalist 'BYOS' build"
  prepare_dist
  execute_tiny_build
  log "Build complete: ${DIST}/keel-min"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_ci_min.log' >&5
}

function validate_args() { :; }

function verify_toolchain() {
  if [[ ! "$(go version)" =~ ${REQUIRED_GO_VERSION} ]]; then
    log "Error: Requires ${REQUIRED_GO_VERSION}"
    exit 1
  fi
}

function execute_tiny_build() {
  local -r tiny_tags='no_acme,no_authz,no_fips,no_h2,no_otel,no_owasp,no_pprof,no_prom,no_remotelog,no_authn,no_h3,no_sidecar,no_statsd'
  CGO_ENABLED=0 \
    go build -v -trimpath -tags "${tiny_tags}" \
    -ldflags='-s -w -buildid=' \
    -o "${DIST}/keel-min" ./cmd/keel
}

function prepare_dist() { mkdir -p "${DIST}"; }

main "${@:-}"
