#!/usr/bin/env bash
# setup-markdownlint.sh
# Install markdownlint-cli on a CI runner.
# No-op if markdownlint is already present at the expected version.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly MARKDOWNLINT_VERSION="0.44.0"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  install_markdownlint
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_setup_markdownlint.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

function already_installed() {
  command -v markdownlint > /dev/null 2>&1 || return 1
  local installed
  installed="$(markdownlint --version 2>/dev/null | tr -d '[:space:]')"
  [ "${installed}" = "${MARKDOWNLINT_VERSION}" ]
}

function install_markdownlint() {
  if already_installed; then
    log "✅ markdownlint ${MARKDOWNLINT_VERSION} already installed"
    return 0
  fi
  log "⚓ Installing markdownlint-cli@${MARKDOWNLINT_VERSION}..."
  npm install --global "markdownlint-cli@${MARKDOWNLINT_VERSION}"
  log "✅ markdownlint ${MARKDOWNLINT_VERSION} installed"
}

main "${@:-}"
