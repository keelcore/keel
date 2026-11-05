#!/usr/bin/env bash
# build-binaries.sh
# Build every release binary concurrently (scripts/lib/parallel.sh): each flavor for each
# architecture, each in its own output directory, then under its release name
# (dist/keel-<flavor>-linux-<arch>). Runs in the dev image (make release-binaries).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/parallel.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/parallel.sh"

readonly BUILDS='.build'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  log 'Building release binaries'
  local -a jobs=()
  local flavor arch
  for flavor in min max fips; do
    for arch in amd64 arm64; do
      jobs+=("${flavor}/${arch}")
    done
  done
  run_parallel build_one "${jobs[@]}"
  rm -rf "dist/${BUILDS}"
  log '✅ Release binaries built'
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_build_binaries.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

# build_one FLAVOR/ARCH: one build, as a run_parallel job.
function build_one() {
  local -r flavor="${1%/*}"
  local -r arch="${1#*/}"
  local -r dir="dist/${BUILDS}/${flavor}-${arch}"
  GOARCH="${arch}" KEEL_DIST_DIR="${dir}" bash "scripts/build/$(build_script "${flavor}")"
  mv "${dir}/keel-${flavor}" "dist/keel-${flavor}-linux-${arch}"
}

function build_script() {
  case "${1}" in
    min) printf 'ci_min.sh' ;;
    max) printf 'ci_max_no_fips.sh' ;;
    fips) printf 'ci_max.sh' ;;
  esac
}

main "${@:-}"
