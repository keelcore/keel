#!/usr/bin/env bash
# setup-docker-macos.sh
# Install Colima + docker CLI and start a Colima VM on a macOS CI runner.
# No-op if docker is already reachable (daemon already running).
# macOS only — exits with error on any other OS.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

function main() {
  exec 5>&1
  validate_args "${@:-}"
  require_macos
  if docker info >/dev/null 2>&1; then
    log '✅ Docker daemon already reachable; skipping Colima setup'
    return 0
  fi
  install_deps
  start_colima
  log "✅ Docker daemon ready: $(docker version --format '{{.Server.Version}}')"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_setup_docker_macos.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

function require_macos() {
  if [[ "$(uname -s)" != 'Darwin' ]]; then
    log "❌ setup-docker-macos.sh is macOS only (got: $(uname -s))"
    exit 1
  fi
}

function install_deps() {
  log '🍺 Installing colima and docker CLI via brew...'
  brew install colima docker
}

function start_colima() {
  log '🚀 Starting Colima (cpu=2, memory=4, arch=arm64)...'
  colima start --arch arm64 --cpu 2 --memory 4 --wait 120
  log '✅ Colima started'
}

main "${@:-}"
