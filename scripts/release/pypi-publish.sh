#!/usr/bin/env bash
# scripts/release/pypi-publish.sh
# Builds the keelcore-standards wheel and sdist for PyPI.
# Version is read from the git tag via hatch-vcs — no manual version management.
# Upload is handled by pypa/gh-action-pypi-publish (Trusted Publisher / OIDC) in CI.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

function main() {
  exec 5>&1
  validate_env
  install_tooling
  build
}

function validate_env() {
  log 'Validating environment...'
  if [ ! -f 'pyproject.toml' ]; then
    log '❌ pyproject.toml not found; run from the repository root'
    exit 1
  fi
  log '✅ Environment valid'
}

function install_tooling() {
  log 'Installing hatch and hatch-vcs...'
  if [ -n "${CI:-}" ]; then
    pip install --quiet hatch hatch-vcs
  else
    command -v hatch >/dev/null 2>&1 || pipx install hatch
  fi
  log '✅ Tooling installed'
}

function build() {
  log 'Building wheel and sdist...'
  hatch build
  log '✅ Artifacts built'
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/pypi-publish.log' >&5
}

main "${@:-}"
