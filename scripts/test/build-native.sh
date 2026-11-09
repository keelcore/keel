#!/usr/bin/env bash
# scripts/test/build-native.sh
# Cross-build everything a native macOS or Windows job runs, in the dev image
# (make build-native OS=darwin|windows ARCH=arm64|amd64). The native job only
# executes the output (scripts/test/test-native.sh): it installs no toolchain.
#
# Output, under target/native/<os>-<arch>/:
#   tests/<pkg>[.exe] + tests.tsv        one Go test binary per package with tests; tests.tsv rows are
#                                        DIR<TAB>BINARY, DIR relative to the repository (. and _root for
#                                        the module root)
#   bats/                                the bats the tool image carries
# plus whatever scripts/test/build-native.local.sh OS ARCH OUT EXT builds for this platform.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/paths.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"

readonly LOCAL_HOOK='scripts/test/build-native.local.sh'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local -r os="${1}" arch="${2}"
  local -r out="target/native/${os}-${arch}"
  local ext=''
  [ "${os}" = 'windows' ] && ext='.exe'
  rm -rf "${out}"
  mkdir -p "${out}/tests" "${out}/bin"
  build_tests "${os}" "${arch}" "${out}" "${ext}"
  copy_bats "${out}"
  # What else the project builds for this platform.
  [ ! -f "${LOCAL_HOOK}" ] || bash "${LOCAL_HOOK}" "${os}" "${arch}" "${out}" "${ext}"
  log "✅ native build for ${os}/${arch}: ${out}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/native_build.log' >&5
}

function validate_args() {
  case "${1:-}:${2:-}" in
    darwin:arm64 | darwin:amd64 | windows:amd64 | windows:arm64) ;;
    *)
      log '❌ Usage: build-native.sh darwin|windows arm64|amd64'
      exit 1
      ;;
  esac
}

# build_tests OS ARCH OUT EXT: a test binary per package that has tests, and tests.tsv: each package's
# directory relative to the repository (. for the module root) and its binary.
function build_tests() {
  local -r os="${1}" arch="${2}" out="${3}" ext="${4}"
  local pkg dir bin
  : > "${out}/tests.tsv"
  while IFS= read -r pkg; do
    [ -n "${pkg}" ] || continue
    dir="$(package_dir "$(go list -f '{{.Dir}}' "${pkg}")")"
    bin="tests/$(binary_name "${dir}")${ext}"
    [ -n "$(go list -f '{{join .TestGoFiles ""}}{{join .XTestGoFiles ""}}' "${pkg}")" ] || continue
    GOOS="${os}" GOARCH="${arch}" CGO_ENABLED=0 go test -c -o "${out}/${bin}" "${pkg}"
    printf '%s\t%s\n' "${dir}" "${bin}" >> "${out}/tests.tsv"
  done < <(go_pkgs)
  log "✅ $(wc -l < "${out}/tests.tsv" | tr -d ' ') test binaries for ${os}/${arch}"
}

# package_dir DIR: DIR relative to the repository, . for the module root; a directory outside it fails.
function package_dir() {
  local -r dir="${1}"
  case "${dir}" in
    "${PWD}") printf '.' ;;
    "${PWD}"/*) printf '%s' "${dir#"${PWD}/"}" ;;
    *)
      log "❌ package directory outside the repository: ${dir}"
      return 1
      ;;
  esac
}

# binary_name DIR: the test binary's name for package directory DIR. The module root is _root: go skips
# directories whose names start with _, so no subpackage's name does.
function binary_name() {
  if [ "${1}" = '.' ]; then
    printf '_root'
  else
    printf '%s' "${1}" | tr '/' '_'
  fi
}

function copy_bats() {
  local -r out="${1}"
  mkdir -p "${out}/bats/bin" "${out}/bats/libexec" "${out}/bats/lib"
  cp -p /usr/local/bin/bats "${out}/bats/bin/"
  cp -Rp /usr/local/libexec/bats-core "${out}/bats/libexec/"
  cp -Rp /usr/local/lib/bats-core "${out}/bats/lib/"
}

main "${@:-}"
