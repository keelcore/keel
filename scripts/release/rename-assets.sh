#!/usr/bin/env bash
# rename-assets.sh
# Append GOOS-GOARCH platform suffix to release binaries in dist/.
# Must be called after all build scripts and before checksum generation.
#
# Example: dist/keel-min → dist/keel-min-linux-amd64

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local goos
  local goarch
  goos="$(go env GOOS)"
  goarch="$(go env GOARCH)"
  log "Renaming artifacts for ${goos}/${goarch}"
  rename_artifacts "${goos}" "${goarch}"
  log "Rename complete"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_rename_assets.log' >&5
}

function validate_args() { :; }

function rename_artifacts() {
  local -r goos="${1}"
  local -r goarch="${2}"
  local artifact
  for artifact in dist/keel-min dist/keel-max dist/keel-fips; do
    [ -f "${artifact}" ] || continue
    rename_one "${artifact}" "${goos}" "${goarch}"
  done
}

function rename_one() {
  local -r src="${1}"
  local -r dst="${src}-${2}-${3}"
  mv "${src}" "${dst}"
  log "  ${src} → ${dst}"
}

main "${@:-}"
