#!/usr/bin/env bash
# ci_min.sh
# Minimalist Build: Smallest standalone footprint for BYOS users.

set -o nounset
set -o errexit
set -o pipefail

readonly REQUIRED_GO_VERSION="go1.23.0"

function main() {
  exec 5>&1
  verify_toolchain
  log "🚀 Starting Minimalist 'BYOS' build"
  # ... (prepare_dist and execute_tiny_build)
}

function verify_toolchain() {
  log "🔍 Verifying toolchain: ${REQUIRED_GO_VERSION}"
  if [[ ! "$(go version)" =~ ${REQUIRED_GO_VERSION} ]]; then
    log "❌ Error: Requires ${REQUIRED_GO_VERSION}"
    exit 1
  fi

  # Ensure we are not accidentally using a BoringCrypto toolchain for Min build
  if [[ "$(go env GOEXPERIMENT)" =~ "boringcrypto" ]]; then
    log "⚠️ Warning: Using BoringCrypto toolchain for a non-FIPS build"
  fi
}

# ... (Rest of script)
main "${@:-}"
