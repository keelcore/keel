#!/usr/bin/env bash
# setup-markdownlint.sh
# Install the pinned markdownlint (markdownlint-cli) with `npm ci` from the
# committed lockfile (scripts/lib/markdownlint.sh says where): every package in
# the tree is the locked version, checked against its locked integrity hash.
# No-op if that lockfile's install is already present.
# Environment: KEELCORE_IMAGE_BUILD (default empty); set while a tool image is built.
# Project hook: setup-markdownlint.local.sh runs first, when present, with this script's arguments
# (scripts/lib/local-hook.sh: the same path, in the repository this runs in).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/local-hook.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/local-hook.sh"

# shellcheck source=../lib/markdownlint.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/markdownlint.sh"

declare -rx KEELCORE_IMAGE_BUILD="${KEELCORE_IMAGE_BUILD:-}"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
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

function install_markdownlint() {
  local lock_dir install_dir version
  lock_dir="$(markdownlint_lock_dir)"
  install_dir="$(markdownlint_install_dir)"
  version="$(jq -r '.dependencies["markdownlint-cli"]' "${lock_dir}/package.json")"
  if [ -x "$(markdownlint_bin)" ] && [ "$("$(markdownlint_bin)" --version | tr -d '[:space:]')" = "${version}" ]; then
    log "✅ markdownlint ${version} already installed (${install_dir})"
    return 0
  fi
  require_image_build
  log "⚓ Installing markdownlint-cli@${version} from ${lock_dir}/package-lock.json..."
  mkdir -p "${install_dir}"
  cp "${lock_dir}/package.json" "${lock_dir}/package-lock.json" "${install_dir}/"
  npm ci --prefix "${install_dir}" --ignore-scripts --no-audit --no-fund
  log "✅ markdownlint ${version} installed (${install_dir})"
}

# require_image_build: tools are installed only while the tool image is built
# (containers/*/Containerfile sets KEELCORE_IMAGE_BUILD); anywhere else the tool is already in the
# image, and a missing or different version is an error, never an install.
function require_image_build() {
  [ -n "${KEELCORE_IMAGE_BUILD}" ] && return 0
  log "❌ markdownlint-cli ${version} is not in this tool image; tools install only when the image is built"
  exit 1
}

main "${@:-}"
