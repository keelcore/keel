#!/usr/bin/env bash
# setup.sh
# P3.5 — Provisions the local k8s harness on Apple's `container` CLI:
#   - a k8s cluster (`container cluster`, kind node image)
#   - keel-toolbox, the harness control plane on the container bridge network.
# kubectl/helm/tests run inside keel-toolbox and reach the cluster
# guest-to-guest; the host only drives the `container` CLI (vsock). Nothing
# depends on host -> guest networking (routing or Local Network privacy).
# Overrides: KEEL_CLUSTER_NAME, KEEL_CLUSTER_CPU, KEEL_CLUSTER_MEMORY, KEEL_TOOLBOX_NAME.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

REPO_ROOT=''
readonly LOG_FILE='/tmp/keel_container_setup.log'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  local -r name="${KEEL_CLUSTER_NAME:-keel-k8s}"
  local -r toolbox="${KEEL_TOOLBOX_NAME:-keel-toolbox}"
  log '🔧 Setting up Keel local k8s environment via Apple container...'
  check_brew
  ensure_tool 'container'
  start_container_system
  build_toolbox
  start_cluster "${name}"
  start_toolbox "${toolbox}"
  install_kubeconfig "${name}" "${toolbox}"
  wait_for_cluster "${name}" "${toolbox}"
  log '✅ Cluster ready. Next: scripts/container/deploy.sh'
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

function check_brew() {
  log '🍺 Checking for Homebrew...'
  command -v brew &> /dev/null && { log '✅ Homebrew found'; return; }
  printf 'Error: Homebrew is required. Install from https://brew.sh\n' >&2
  exit 1
}

function ensure_tool() {
  local -r cmd="${1}"
  command -v "${cmd}" &> /dev/null && return 0
  log "📦 Installing ${cmd} via Homebrew..."
  brew install "${cmd}"
  log "✅ ${cmd} installed"
}

function start_container_system() {
  container system status > /dev/null 2>&1 && { log '✅ container system running'; return; }
  log '🚀 Starting Apple container system service...'
  container system start
  log '✅ container system started'
}

function build_toolbox() {
  log '🧰 Building keel-toolbox image...'
  container build -t 'keel-toolbox:latest' \
    -f "${REPO_ROOT}/scripts/container/toolbox/Containerfile" \
    "${REPO_ROOT}/scripts/container/toolbox"
  log '✅ keel-toolbox image built'
}

function is_running() {
  local -r id="${1}"
  container ls | awk -v id="${id}" 'NR > 1 && $1 == id { found = 1 } END { exit !found }'
}

function start_cluster() {
  local -r name="${1}"
  if is_running "${name}"; then
    log "✅ Cluster '${name}' already running; reusing it"
    return 0
  fi
  log "🚀 Creating container cluster '${name}'..."
  container cluster create \
    --name "${name}" \
    --cpus "${KEEL_CLUSTER_CPU:-4}" \
    --memory "${KEEL_CLUSTER_MEMORY:-6G}"
  log "✅ Cluster '${name}' created"
}

function start_toolbox() {
  local -r toolbox="${1}"
  if is_running "${toolbox}"; then
    log "✅ ${toolbox} already running"
    return 0
  fi
  container rm -f "${toolbox}" > /dev/null 2>&1 || true
  log "🧰 Starting ${toolbox} (repo mounted at /work)..."
  container run -d --name "${toolbox}" -v "${REPO_ROOT}:/work" -w /work \
    'keel-toolbox:latest' sleep infinity
  log "✅ ${toolbox} started"
}

# install_kubeconfig: the node's admin.conf already targets the node IP, which
# the toolbox reaches on the bridge. Copied over vsock (container exec).
function install_kubeconfig() {
  local -r name="${1}" toolbox="${2}"
  log "🔑 Installing kubeconfig for '${name}' into ${toolbox}..."
  container exec "${name}" cat /etc/kubernetes/admin.conf \
    | container exec -i "${toolbox}" sh -c \
      'mkdir -p /root/.kube && cat > /root/.kube/config && chmod 600 /root/.kube/config'
  container exec "${toolbox}" sh -c \
    "kubectl config rename-context \"\$(kubectl config current-context)\" '${name}' > /dev/null"
  log "✅ kubectl context: ${name}"
}

function wait_for_cluster() {
  local -r name="${1}" toolbox="${2}"
  log "⏳ Waiting for cluster nodes to be Ready (context: ${name})..."
  container exec "${toolbox}" kubectl --context "${name}" wait \
    --for=condition=Ready node --all --timeout=120s
  log '✅ All cluster nodes are Ready'
}

main "${@:-}"
