#!/usr/bin/env bash
# ci_max_no_fips.sh
# Builds a full-feature standalone binary without FIPS/BoringSSL.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly REQUIRED_GO_VERSION="go1.2"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  verify_toolchain
  log "Starting Max No-FIPS build"
  prepare_dist
  execute_standard_build
  log "Build complete: dist/keel-max"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_ci_max_no_fips.log' >&5
}

function validate_args() { :; }

function verify_toolchain() {
  if [[ ! "$(go version)" =~ ${REQUIRED_GO_VERSION} ]]; then
    log "Error: Requires ${REQUIRED_GO_VERSION}"
    exit 1
  fi
}

function execute_standard_build() {
  CGO_ENABLED=0 \
    go build -v -trimpath -tags 'no_fips' \
    -ldflags='-s -w -buildid=' \
    -o 'dist/keel-max' ./cmd/keel
}

function prepare_dist() { mkdir -p 'dist'; }

main "${@:-}"
