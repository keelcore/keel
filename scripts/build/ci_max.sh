#!/usr/bin/env bash
# ci_max.sh
# Builds a statically-linked, FIPS-compatible Keel binary.

# bash configuration per Google Style Guide:
set -o nounset
set -o errexit
set -o pipefail

# Constants for pinned toolchain
readonly REQUIRED_GO_VERSION="go1.23.0"
readonly REQUIRED_GO_EXP="boringcrypto"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  verify_toolchain

  log "🛡️ Starting Corporate FIPS build (Static CGO)"
  prepare_dist
  execute_fips_build
  verify_fips_symbols
}

function log() {
  local msg="${1:-}"
  printf '%s\n' "${msg}" >&5
}

function verify_toolchain() {
  log "🔍 Verifying toolchain: ${REQUIRED_GO_VERSION} with ${REQUIRED_GO_EXP}"

  # Check Go version and BoringCrypto experiment
  if [[ ! "$(go version)" =~ ${REQUIRED_GO_VERSION} ]]; then
    log "❌ Error: Requires ${REQUIRED_GO_VERSION}"
    exit 1
  fi

  if [[ ! "$(go env GOEXPERIMENT)" =~ ${REQUIRED_GO_EXP} ]]; then
    log "❌ Error: GOEXPERIMENT=${REQUIRED_GO_EXP} not active"
    exit 1
  fi

  # Max build requires a C compiler for static linking
  if ! command -v gcc >/dev/null 2>&1; then
    log "❌ Error: gcc is required for static CGO linking"
    exit 1
  fi
}

function execute_fips_build() {
  # -linkmode external + -static ensures no dynamic libc dependency
  GOEXPERIMENT=boringcrypto CGO_ENABLED=1 \
    go build -v \
    -ldflags='-linkmode external -extldflags "-static" -s -w' \
    -o 'dist/keel-fips' ./cmd/keel
}

function verify_fips_symbols() {
  log "🧪 Verifying BoringCrypto symbols..."
  if ! go tool nm "dist/keel-fips" | grep -q "_Cfunc__goboringcrypto_"; then
    log "❌ Error: FIPS symbols missing from binary!"
    exit 1
  fi
  log "✅ FIPS verification successful"
}

# Functions omitted for brevity (validate_args, prepare_dist)...

main "${@:-}"
