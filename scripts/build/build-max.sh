#!/usr/bin/env bash
# build-max.sh
# Corporate Default: Builds a statically-linked, FIPS-compatible Keel binary.
# Env: KEEL_DIST_DIR (default dist).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# Output directory; scripts/release/build-binaries.sh gives each architecture its own.
declare -rx KEEL_DIST_DIR="${KEEL_DIST_DIR:-dist}"

readonly REQUIRED_GO_VERSION="go1.2"
# Go Cryptographic Module version: v1.0.0 is the CMVP-certified module (cert #5247).
# Pin it explicitly (not `latest`, which is uncertified); bump deliberately.
readonly FIPS_MODULE='v1.0.0'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  verify_toolchain
  log "Starting Corporate FIPS build (Static CGO)"
  prepare_dist
  execute_fips_build
  verify_fips_symbols
  log "Build complete: ${KEEL_DIST_DIR}/keel-fips"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_ci_max.log' >&5
}

function validate_args() { :; }

function verify_toolchain() {
  log "Verifying toolchain: ${REQUIRED_GO_VERSION} (native FIPS-capable toolchain)"
  if [[ ! "$(go version)" =~ ${REQUIRED_GO_VERSION} ]]; then
    log "Error: Requires ${REQUIRED_GO_VERSION}"
    exit 1
  fi
}

function execute_fips_build() {
  log "Compiling with the Go Cryptographic Module ${FIPS_MODULE} (GOFIPS140)"
  # NOTE: HTTP/3 is not compatible with fips140=only; compile it out for FIPS builds.
  GOFIPS140="${FIPS_MODULE}" CGO_ENABLED=0 \
    go build -v -trimpath -tags "fips,no_h3" \
    -ldflags='-s -w -buildid=' \
    -o "${KEEL_DIST_DIR}/keel-fips" ./cmd/keel
}

function verify_fips_symbols() {
  # A cross build (e.g. linux/arm64 on an amd64 runner) cannot execute its test
  # binaries; the host-arch build of the same source runs this check.
  if [ "$(go env GOARCH)" != "$(go env GOHOSTARCH)" ]; then
    log "Skipping FIPS test run for cross build ($(go env GOARCH) on $(go env GOHOSTARCH))"
    return 0
  fi
  log "Verifying FIPS enforcement via test run (GODEBUG=fips140=only)"
  GOFIPS140="${FIPS_MODULE}" GODEBUG=fips140=only \
    go test -v -count=1 -tags "no_h3" ./...
}

function prepare_dist() { mkdir -p "${KEEL_DIST_DIR}"; }

main "${@:-}"
