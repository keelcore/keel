#!/usr/bin/env bash
# ci_max.sh
# Corporate Default: Builds a statically-linked, FIPS-compatible Keel binary.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

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
  log "✅ Authorized Keel Build: Meets Shredded & Hardened Standards"
}

function log() {
  local msg="${1:-}"
  printf '%s\n' "${msg}" >&5
}

function verify_toolchain() {
  log "🔍 Verifying toolchain: ${REQUIRED_GO_VERSION} with ${REQUIRED_GO_EXP}"
  if [[ ! "$(go version)" =~ ${REQUIRED_GO_VERSION} ]]; then
    log "❌ Error: Requires ${REQUIRED_GO_VERSION}"
    exit 1
  fi
  if [[ ! "$(go env GOEXPERIMENT)" =~ ${REQUIRED_GO_EXP} ]]; then
    log "❌ Error: GOEXPERIMENT=${REQUIRED_GO_EXP} not active"
    exit 1
  fi
  if ! command -v gcc >/dev/null 2>&1; then
    log "❌ Error: gcc required for static CGO"
    exit 1
  fi
}

function execute_fips_build() {
  log "🛠️ Compiling from source with BoringCrypto"
  GOEXPERIMENT=boringcrypto CGO_ENABLED=1 \
    go build -v \
    -ldflags='-linkmode external -extldflags "-static" -s -w' \
    -o 'dist/keel-fips' ./cmd/keel
}

function verify_fips_symbols() {
  if ! go tool nm "dist/keel-fips" | grep -q "_Cfunc__goboringcrypto_"; then
    log "❌ Error: FIPS symbols missing!"
    exit 1
  fi
}

function validate_args() { :; }
function prepare_dist() { mkdir -p 'dist'; }

main "${@:-}"
