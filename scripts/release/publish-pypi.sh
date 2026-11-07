#!/usr/bin/env bash
# scripts/release/publish-pypi.sh
# Builds the keelcore-standards wheel and sdist for PyPI.
# Version is read from the git tag via hatch-vcs — no manual version management.
# Upload is handled by pypa/gh-action-pypi-publish (Trusted Publisher / OIDC) in CI.
# Runs in the PyPI build image (containers/pypi), which carries CPython and the hash-locked
# build tooling (scripts/release/pypi/requirements-build.txt); the build runs without
# isolation, so nothing else is installed. Artifacts: target/dist/.

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

function build() {
  log 'Building wheel and sdist...'
  python -m build --no-isolation --outdir target/dist
  log '✅ Artifacts built'
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/pypi-publish.log' >&5
}

main "${@:-}"
