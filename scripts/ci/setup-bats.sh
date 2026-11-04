#!/usr/bin/env bash
# scripts/test/setup-bats.sh
# Install the pinned bats-core (Linux; runs when the dev image is built): the
# release tag, cloned and verified against its committed commit SHA (the release
# publishes no assets to checksum). No-op if the pinned version is present.
# Then runs scripts/ci/setup-bats.local.sh if present, allowing each consumer to
# install extra tools its BATS suites need without modifying the canonical script.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly BATS_VERSION='1.14.0'
readonly BATS_COMMIT='eb7f42f8d608ac693d7a4b67474f6714ea68cfc5'
readonly BATS_REPO='https://github.com/bats-core/bats-core.git'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  install_bats
  if [ -f scripts/ci/setup-bats.local.sh ]; then
    log '🔧 Delegating to scripts/ci/setup-bats.local.sh'
    bash scripts/ci/setup-bats.local.sh
  fi
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/setup_bats.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

function as_root() {
  if [ "$(id -u)" -eq 0 ]; then "$@"; else sudo "$@"; fi
}

function install_bats() {
  if [[ "$(uname -s)" != 'Linux' ]]; then
    log '❌ bats installs in the dev image (Linux); run: make test'
    exit 1
  fi
  install_bats_pinned
  log '✅ bats installed'
}

function install_bats_pinned() {
  if command -v bats > /dev/null 2>&1 && [ "$(bats --version)" = "Bats ${BATS_VERSION}" ]; then
    log "✅ bats ${BATS_VERSION} already installed"
    return 0
  fi
  local tmp head
  tmp="$(mktemp -d)"
  log "⚓ Installing bats ${BATS_VERSION} (${BATS_COMMIT})..."
  git clone --quiet --depth 1 --branch "v${BATS_VERSION}" "${BATS_REPO}" "${tmp}/bats-core"
  head="$(git -C "${tmp}/bats-core" rev-parse HEAD)"
  if [ "${head}" != "${BATS_COMMIT}" ]; then
    log "❌ bats v${BATS_VERSION} resolved to ${head}, expected ${BATS_COMMIT}"
    rm -rf "${tmp}"
    exit 1
  fi
  as_root "${tmp}/bats-core/install.sh" /usr/local
  rm -rf "${tmp}"
}

main "${@:-}"
