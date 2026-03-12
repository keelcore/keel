#!/usr/bin/env bash
# kind-setup.sh
# Creates the keel-ci kind cluster for k8s integration testing.
# Linux: installs pinned kind v0.31.0 (SHA-verified).
# macOS: ensures kind via brew.
# Both: creates cluster keel-ci with kubeconfig at /tmp/keel-kind.kubeconfig.

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
readonly CLUSTER_NAME='keel-ci'
readonly KUBECONFIG_PATH='/tmp/keel-kind.kubeconfig'
readonly LOG_FILE='/tmp/keel_kind_setup.log'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  log "⎈ Setting up kind cluster '${CLUSTER_NAME}'..."
  ensure_kind
  ensure_cluster
  wait_nodes_ready
  log "✅ kind cluster '${CLUSTER_NAME}' is ready"
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

function ensure_kind() {
  local os
  os="$(uname -s)"
  if [[ "${os}" == 'Darwin' ]]; then
    ensure_kind_macos
  elif [[ "${os}" == 'Linux' ]]; then
    ensure_kind_linux
  else
    log "❌ Unsupported OS: ${os}"
    exit 1
  fi
}

function ensure_kind_macos() {
  if command -v kind >/dev/null 2>&1; then
    log "✅ kind already installed: $(kind version)"
    return 0
  fi
  log '🍺 Installing kind via brew...'
  brew install kind
  log "✅ kind installed: $(kind version)"
}

function ensure_kind_linux() {
  if is_pinned_version_installed; then
    log "✅ kind v${KIND_VERSION} already installed"
    return 0
  fi
  log "⬇️  Installing kind v${KIND_VERSION}..."
  download_and_verify
  install_kind
  log "✅ kind v${KIND_VERSION} installed at ${KIND_INSTALL_PATH}"
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
    log "✅ kind cluster '${CLUSTER_NAME}' already exists"
    return 0
  fi
  log "🏗️  Creating kind cluster '${CLUSTER_NAME}'..."
  kind create cluster --name "${CLUSTER_NAME}" \
    --kubeconfig "${KUBECONFIG_PATH}" \
    --wait '60s'
  log "✅ kind cluster '${CLUSTER_NAME}' created"
}

function wait_nodes_ready() {
  log '⏳ Waiting for all nodes to be Ready...'
  kubectl --kubeconfig "${KUBECONFIG_PATH}" wait \
    --for=condition=Ready nodes \
    --all \
    --timeout=120s
  log '✅ All nodes are Ready'
}

main "${@:-}"
