#!/usr/bin/env bash
# clean.local.sh
# Project artifact removal invoked by the canonical scripts/clean.sh (`make clean`).
# Removes keel's derived build outputs so a subsequent build starts from source
# alone: the dist/ release tree, the target/ tree (coverage tracefiles), and the
# Go build cache. Vendored trees, submodules, and tracked source are never touched.
#
# Run locally:  make clean

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

declare ROOT
ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
declare -r ROOT

function main() {
  exec 5>&1
  remove 'dist'
  remove 'target'
  log '🧹 go clean'
  go clean
  log '✅ clean complete'
}

# remove deletes one repo-relative path (file or directory) if it exists, reporting what it removed.
function remove() {
  local rel="${1}"
  local path="${ROOT}/${rel}"
  if [ -e "${path}" ]; then
    log "🧹 removing ${rel}"
    rm -rf "${path}"
  fi
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_clean_local.log' >&5
}

main "${@:-}"
