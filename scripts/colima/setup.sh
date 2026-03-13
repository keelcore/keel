#!/usr/bin/env bash
# setup.sh
# P3.5 — Installs prerequisites and starts a local k8s cluster via Colima on macOS.
# Resource defaults can be overridden with KEEL_COLIMA_CPU, KEEL_COLIMA_MEMORY,
# KEEL_COLIMA_DISK, and KEEL_COLIMA_PROFILE environment variables.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly LOG_FILE='/tmp/keel_colima_setup.log'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  log '🔧 Setting up Keel local k8s environment via Colima...'
  check_brew
  install_prerequisites
  start_cluster
  wait_for_cluster
  log '✅ Cluster ready. Next: scripts/colima/deploy.sh'
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a "${LOG_FILE}" >&5
}

function validate_args() {
  if [ "${#}" -gt 0 ] && [ -z "${1:-}" ]; then
    log '❌ Error: Unexpected empty argument'
    exit 1
  fi
}

function check_brew() {
  log '🍺 Checking for Homebrew...'
  command -v brew &> /dev/null && { log '✅ Homebrew found'; return; }
  printf 'Error: Homebrew is required. Install from https://brew.sh\n' >&2
  exit 1
}

function install_prerequisites() {
  log '📦 Checking required tools...'
  ensure_tool 'colima'  'colima'
  ensure_tool 'docker'  'docker'
  ensure_tool 'kubectl' 'kubectl'
  ensure_tool 'helm'    'helm'
  log '✅ All prerequisites satisfied'
}

function ensure_tool() {
  local -r cmd="${1}"
  local -r pkg="${2}"
  command -v "${cmd}" &> /dev/null && return 0
  log "📦 Installing ${pkg} via Homebrew..."
  brew install "${pkg}"
  log "✅ ${pkg} installed"
}

function start_cluster() {
  local -r profile="${KEEL_COLIMA_PROFILE:-keel-k8s}"
  log "🚀 Starting Colima k8s cluster (profile: ${profile})..."
  colima_start "${profile}"
  log "✅ Colima started (profile: ${profile})"
}

function colima_start() {
  local -r profile="${1}"
  local -r cpu="${KEEL_COLIMA_CPU:-4}"
  local -r mem="${KEEL_COLIMA_MEMORY:-8}"
  local -r disk="${KEEL_COLIMA_DISK:-60}"
  colima start "${profile}" \
    --kubernetes \
    --cpu "${cpu}" \
    --memory "${mem}" \
    --disk "${disk}" \
    --kubernetes-version 'v1.29.0' \
    --runtime docker
}

function wait_for_cluster() {
  local -r profile="${KEEL_COLIMA_PROFILE:-keel-k8s}"
  local -r context="colima-${profile}"
  log "⏳ Waiting for cluster nodes to be Ready (context: ${context})..."
  kubectl --context "${context}" wait \
    --for=condition=Ready node --all \
    --timeout=120s
  log "✅ All cluster nodes are Ready"
  log "   kubectl context: ${context}"
}

main "${@:-}"
