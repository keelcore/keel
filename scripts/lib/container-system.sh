#!/usr/bin/env bash
# scripts/lib/container-system.sh
# The container runtime of this host and the state of Apple container's system, shared by
# scripts/lib/dev-image.sh (runs and builds tool images), scripts/ci/setup-apple-container.sh (installs it),
# scripts/ci/ensure-container.sh (make ensure-container) and scripts/run-pre-commit.sh (the commit hook,
# which uses no network). The caller defines log. Source this file.
# Environment: KEELCORE_DEV_IMAGE (default empty); set inside a tool image.

# runtime: the container runtime of this host.
declare -rx KEELCORE_DEV_IMAGE="${KEELCORE_DEV_IMAGE:-}"

function runtime() {
  if [ "$(uname -s)" = 'Darwin' ]; then printf 'container'; else printf 'docker'; fi
}

# uses_apple_container: this host runs tool images with Apple container; inside a tool image nothing does.
function uses_apple_container() {
  [ -z "${KEELCORE_DEV_IMAGE}" ] && [ "$(runtime)" = 'container' ]
}

# start_container_system: a running system is left as it is, so its image builder and build cache survive; a
# stopped one is started, installing its default kernel when it has none.
function start_container_system() {
  if container system status > /dev/null 2>&1; then
    log '✅ container system already running'
    return 0
  fi
  container system start --enable-kernel-install
  log '✅ container system running, with its kernel'
}

# require_container_system: offline: fails, naming the make target that fixes it, when Apple container is
# missing or its system is stopped; starts and fetches nothing.
function require_container_system() {
  uses_apple_container || return 0
  if ! command -v container > /dev/null 2>&1; then
    log '❌ Apple container is not installed: run make setup-apple-container'
    return 1
  fi
  if ! container system status > /dev/null 2>&1; then
    log '❌ the container system is not running: run make ensure-container'
    return 1
  fi
}
