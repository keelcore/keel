#!/usr/bin/env bash
# setup.sh
# P3.5 — Provisions the local k8s harness on Apple's `container` CLI:
#   - a k8s cluster (`container cluster`, kind node image) whose nodes pull from the local image store
#   - keel-toolbox, the harness control plane on the container bridge network.
# kubectl/helm/tests run inside keel-toolbox and reach the cluster
# guest-to-guest; the host only drives the `container` CLI (vsock). Nothing
# depends on host -> guest networking (routing or Local Network privacy).
# Env: KEEL_CLUSTER_NAME (default keel-k8s), KEEL_CLUSTER_CPU (default 4), KEEL_CLUSTER_MEMORY
# (default 6G), KEEL_TOOLBOX_NAME (default keel-toolbox), KEELCORE_RUN_SH (default scripts/dev/run.sh, run
# from the repository root; a seam, so tests stub it).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

REPO_ROOT=''
# The toolbox tool image (config/tool-images.local.txt), named by its build context; set by fetch_toolbox.
TOOLBOX_IMAGE=''
readonly LOG_FILE='/tmp/keel_container_setup.log'

declare -rx KEEL_CLUSTER_NAME="${KEEL_CLUSTER_NAME:-keel-k8s}"
declare -rx KEEL_CLUSTER_CPU="${KEEL_CLUSTER_CPU:-4}"
declare -rx KEEL_CLUSTER_MEMORY="${KEEL_CLUSTER_MEMORY:-6G}"
declare -rx KEEL_TOOLBOX_NAME="${KEEL_TOOLBOX_NAME:-keel-toolbox}"
declare -rx KEELCORE_RUN_SH="${KEELCORE_RUN_SH:-scripts/dev/run.sh}"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  log '🔧 Setting up Keel local k8s environment via Apple container...'
  install_container
  fetch_toolbox
  start_cluster "${KEEL_CLUSTER_NAME}"
  start_toolbox "${KEEL_TOOLBOX_NAME}"
  install_kubeconfig "${KEEL_CLUSTER_NAME}" "${KEEL_TOOLBOX_NAME}"
  wait_for_cluster "${KEEL_CLUSTER_NAME}" "${KEEL_TOOLBOX_NAME}"
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

# install_container: Apple's signed package of the latest release; the system started, with its kernel.
function install_container() {
  log '📦 Installing Apple container...'
  make -C "${REPO_ROOT}" --no-print-directory setup-apple-container
}

# fetch_toolbox: the toolbox tool image of this checkout's build context, pulled when published, else built
# locally from that context (scripts/dev/run.sh).
function fetch_toolbox() {
  log '🧰 Fetching the toolbox tool image...'
  (cd "${REPO_ROOT}" && bash "${KEELCORE_RUN_SH}" --image toolbox --fetch-image)
  TOOLBOX_IMAGE="$(cd "${REPO_ROOT}" && bash "${KEELCORE_RUN_SH}" --image toolbox --image-ref)"
  log "✅ toolbox image: ${TOOLBOX_IMAGE}"
}

function is_running() {
  local -r id="${1}"
  container inspect "${id}" 2> /dev/null | jq -e '.[0].status.state == "running"' > /dev/null
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
    --cpus "${KEEL_CLUSTER_CPU}" \
    --memory "${KEEL_CLUSTER_MEMORY}" \
    --shared-image-store
  log "✅ Cluster '${name}' created"
}

# mounted_repo NAME: the host directory NAME mounts at /work (physical path), empty when it is gone.
function mounted_repo() {
  local src
  src="$(container inspect "${1}" 2> /dev/null \
    | jq -r '.[0].configuration.mounts[]? | select(.destination == "/work") | .source' 2> /dev/null)"
  [ -n "${src}" ] && (cd "${src}" 2> /dev/null && pwd -P)
}

function start_toolbox() {
  local -r toolbox="${1}"
  # Reused only while it mounts this checkout: a toolbox left by another checkout (an
  # update-pins worktree, since removed) is restarted on this one; the cluster is kept.
  if is_running "${toolbox}" && [ "$(mounted_repo "${toolbox}")" = "$(cd "${REPO_ROOT}" && pwd -P)" ]; then
    log "✅ ${toolbox} already running"
    return 0
  fi
  container rm -f "${toolbox}" > /dev/null 2>&1 || true
  log "🧰 Starting ${toolbox} (repo mounted at /work)..."
  container run -d --name "${toolbox}" -v "${REPO_ROOT}:/work" -w /work \
    "${TOOLBOX_IMAGE}" sleep infinity
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
