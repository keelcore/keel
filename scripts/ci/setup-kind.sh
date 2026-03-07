#!/usr/bin/env bash
# setup-kind.sh
# Install kind and create a single-node cluster for kubectl dry-run validation.
# Idempotent: no-op if the pinned kind version is already installed and the
# cluster already exists.
#
# Pinned version: v0.31.0
# Update KIND_VERSION and KIND_SHA256 together when bumping.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly KIND_VERSION='0.31.0'
readonly KIND_SHA256='eb244cbafcc157dff60cf68693c14c9a75c4e6e6fedaf9cd71c58117cb93e3fa'
readonly KIND_URL="https://github.com/kubernetes-sigs/kind/releases/download/v${KIND_VERSION}/kind-linux-amd64"
readonly KIND_INSTALL_PATH='/usr/local/bin/kind'
readonly CLUSTER_NAME='keel-lint'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  require_linux
  ensure_kind
  ensure_cluster
  log "kind cluster '${CLUSTER_NAME}' is ready"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_setup_kind.log' >&5
}

function validate_args() { :; }

function require_linux() {
  if [[ "$(uname -s)" != 'Linux' ]]; then
    log "ERROR: setup-kind.sh only supports Linux CI runners"
    exit 1
  fi
}

function ensure_kind() {
  if is_pinned_version_installed; then
    log "kind v${KIND_VERSION} already installed"
    return 0
  fi
  log "Installing kind v${KIND_VERSION}"
  download_and_verify
  install_kind
  log "kind v${KIND_VERSION} installed at ${KIND_INSTALL_PATH}"
}

function is_pinned_version_installed() {
  command -v kind >/dev/null 2>&1 && \
    kind version 2>/dev/null | grep -qF "v${KIND_VERSION}"
}

function download_and_verify() {
  local tmp
  tmp="$(mktemp -d)"
  curl -fsSL "${KIND_URL}" -o "${tmp}/kind-linux-amd64"
  printf '%s  %s/kind-linux-amd64\n' "${KIND_SHA256}" "${tmp}" \
    | sha256sum --check --quiet
  mv "${tmp}/kind-linux-amd64" /tmp/kind-staged
  rm -rf "${tmp}"
}

function install_kind() {
  sudo mv /tmp/kind-staged "${KIND_INSTALL_PATH}"
  sudo chmod +x "${KIND_INSTALL_PATH}"
}

function ensure_cluster() {
  if kind get clusters 2>/dev/null | grep -qF "${CLUSTER_NAME}"; then
    log "kind cluster '${CLUSTER_NAME}' already exists"
    return 0
  fi
  log "Creating kind cluster '${CLUSTER_NAME}'"
  kind create cluster --name "${CLUSTER_NAME}" --wait '60s'
}

main "${@:-}"
