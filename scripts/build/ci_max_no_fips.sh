#!/usr/bin/env bash
# ci_max_no_fips.sh
# Builds a full-feature standalone binary without FIPS/BoringSSL.

set -o nounset
set -o errexit
set -o pipefail

readonly REQUIRED_GO_VERSION="go1.23.0"

function main() {
  exec 5>&1
  verify_toolchain
  log "⚙️ Starting Max No-FIPS build"
  # ... (prepare_dist and execute_standard_build)
}

function verify_toolchain() {
  if [[ ! "$(go version)" =~ ${REQUIRED_GO_VERSION} ]]; then
    log "❌ Error: Requires ${REQUIRED_GO_VERSION}"
    exit 1
  fi
}

# ... (Rest of script)
main "${@:-}"
