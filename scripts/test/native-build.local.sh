#!/usr/bin/env bash
# scripts/test/native-build.local.sh OS ARCH OUT EXT
# keel's part of a native build: keel-min and keel-max in dist/, and the pebble setup-pebble.sh pins.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

function main() {
  local -r os="${1}" arch="${2}" out="${3}" ext="${4}"
  build_keel "${os}" "${arch}" "${ext}"
  build_pebble "${os}" "${arch}" "${out}" "${ext}"
}

function build_keel() {
  local -r os="${1}" arch="${2}" ext="${3}"
  local variant
  GOOS="${os}" GOARCH="${arch}" bash scripts/build/ci_min.sh
  GOOS="${os}" GOARCH="${arch}" bash scripts/build/ci_max_no_fips.sh
  if [ -n "${ext}" ]; then
    for variant in keel-min keel-max; do
      mv "dist/${variant}" "dist/${variant}${ext}"
    done
  fi
}

# build_pebble OS ARCH OUT EXT: the pebble version setup-pebble.sh pins, cross-built.
function build_pebble() {
  local -r os="${1}" arch="${2}" out="${3}" ext="${4}"
  local module
  module="$(sed -nE "s/^readonly PEBBLE_MODULE='([^']+)'\$/\\1/p" scripts/ci/setup-pebble.sh)"
  GOOS="${os}" GOARCH="${arch}" CGO_ENABLED=0 go install "${module}"
  cp "$(go env GOPATH)/bin/${os}_${arch}/pebble${ext}" "${out}/bin/pebble${ext}"
}

main "${@:-}"
