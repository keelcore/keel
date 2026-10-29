#!/usr/bin/env bash
# host-network.sh
# Enforces the host <-> guest route contract for Apple's `container` CLI on
# macOS (`make container-host-network`). One invariant:
#   1. Subnet route (apple/container#1881): container installs only an
#      interface-scoped default on its bridge, so after a VPN/Tailscale route
#      change the subnet resolves via the wrong interface. Enforced by
#      restarting the container system service (no sudo), which recreates it.
#
# Idempotent: the route is checked first and left alone when it holds.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly LOG_FILE='/tmp/keel_container_host_network.log'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  require_macos
  require_tool 'container'
  require_tool 'jq'
  local subnet gateway
  subnet="$(network_field 'ipv4Subnet')"
  gateway="$(network_field 'ipv4Gateway')"
  log "🔍 container network: subnet=${subnet} gateway=${gateway}"
  ensure_route "${subnet}" "${gateway}"
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

function require_macos() {
  if [[ "$(uname -s)" != 'Darwin' ]]; then
    log "❌ host-network.sh is macOS only (got: $(uname -s))"
    exit 1
  fi
}

function require_tool() {
  local -r cmd="${1}"
  command -v "${cmd}" &> /dev/null && return 0
  log "❌ ${cmd} not found; install with: brew install ${cmd}"
  exit 1
}

function network_field() {
  local -r field="${1}"
  local value
  value="$(container network inspect default | jq -r ".[0].status.${field} // empty")"
  [ -n "${value}" ] || { log "❌ container network 'default' has no ${field}; run: container system start"; exit 1; }
  printf '%s\n' "${value}"
}

# bridge_for_gateway: the host interface that owns the gateway address. It only
# exists while at least one container is running.
function bridge_for_gateway() {
  local -r gateway="${1}"
  ifconfig -a | awk -v gw="${gateway}" '
    /^[a-z]/ { iface = $1; sub(":", "", iface) }
    $1 == "inet" && $2 == gw { print iface; exit }'
}

function ensure_route() {
  local -r subnet="${1}" gateway="${2}"
  local bridge probe iface
  bridge="$(bridge_for_gateway "${gateway}")"
  if [ -z "${bridge}" ]; then
    log 'ℹ️  No container bridge up (no running containers); route check skipped'
    return 0
  fi
  probe="$(printf '%s\n' "${subnet%/*}" | awk -F. '{ printf "%s.%s.%s.2", $1, $2, $3 }')"
  iface="$(route -n get "${probe}" | awk '/interface:/ { print $2 }')"
  if [ "${iface}" = "${bridge}" ]; then
    log "✅ Route OK: ${subnet} resolves via ${bridge}"
    return 0
  fi
  log "⚠️  Stale route: ${subnet} resolves via '${iface}', expected '${bridge}'"
  log '🔄 Restarting container system to recreate the bridge route...'
  container system stop
  container system start
  log '✅ container system restarted; the route is installed when the next container starts'
}

main "${@:-}"
