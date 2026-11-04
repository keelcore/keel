#!/usr/bin/env bash
# go.sh
# Check that every repo-owned Go source file is gofmt-clean, then run go vet and
# staticcheck (pinned by scripts/ci/setup-staticcheck.sh) in every repo-owned Go module.
#
# Scoped to superproject-tracked *.go via go_source_files (lib/paths.sh), so
# vendored and submodule trees (vendor/, vendors/, .standards/) are excluded —
# their formatting is the upstream's concern, not ours. This is the gate that was
# missing: Go edits (including test files added by a coverage ratchet) could reach
# a commit with no gofmt check in the lint set.
#
# Exits 1 on any gofmt, go vet or staticcheck finding (CI mode).
# Pass --fix to rewrite unformatted files in place instead; vet and staticcheck do not run.
#
# Run locally:  bash scripts/lint/go.sh
# Auto-fix:     bash scripts/lint/go.sh --fix

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/paths.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local fix=0
  [ "${1:-}" = "--fix" ] && fix=1
  check_gofmt "${fix}"
  [ "${fix}" -eq 1 ] && return 0
  check_modules
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_lint_go.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] && [ -n "${1:-}" ]; then
    log '❌ Usage: go.sh [--fix]'
    exit 1
  fi
  if [ "${#}" -eq 1 ] && [ -n "${1:-}" ] && [ "${1}" != "--fix" ]; then
    log "❌ Unknown argument: ${1}"
    log '❌ Usage: go.sh [--fix]'
    exit 1
  fi
}

function ensure_gofmt() {
  command -v gofmt >/dev/null 2>&1 && return 0
  log '❌ gofmt not found on PATH (install the Go toolchain)'
  exit 1
}

function check_gofmt() {
  local -r fix="${1}"
  ensure_gofmt
  local files
  files="$(go_source_files)"
  if [ -z "${files}" ]; then
    log '✅ gofmt: no Go source files to check'
    return 0
  fi
  if [ "${fix}" -eq 1 ]; then
    printf '%s' "${files}" | tr '\n' '\0' | xargs -0 gofmt -w
    log '✅ gofmt: Go sources formatted'
    return 0
  fi
  local unformatted
  unformatted="$(printf '%s' "${files}" | tr '\n' '\0' | xargs -0 gofmt -l)"
  if [ -n "${unformatted}" ]; then
    log '❌ gofmt: these Go files are not formatted:'
    printf '%s\n' "${unformatted}" | while IFS= read -r offender; do log "   ${offender}"; done
    log '   Fix with: bash scripts/lint/go.sh --fix'
    exit 1
  fi
  log '✅ gofmt: all Go sources formatted'
}

# go_bin: where `go install` puts binaries (GOBIN, else the first GOPATH entry's bin).
function go_bin() {
  local gobin
  gobin="$(go env GOBIN)"
  [ -n "${gobin}" ] || gobin="$(go env GOPATH | cut -d: -f1)/bin"
  printf '%s\n' "${gobin}"
}

# ensure_staticcheck: the pinned staticcheck (scripts/ci/setup-staticcheck.sh, a release binary on
# PATH in the tool image), else one in the Go install directory whether or not that is on PATH.
function ensure_staticcheck() {
  command -v staticcheck > /dev/null 2>&1 && return 0
  local gobin
  gobin="$(go_bin)"
  if [ -x "${gobin}/staticcheck" ]; then
    PATH="${gobin}:${PATH}"
    return 0
  fi
  log "❌ staticcheck not found on PATH or in ${gobin} (it is in the tool image: run through make)"
  exit 1
}

# go_module_dirs: directories of the repo-owned go.mod files (vendored and submodule trees excluded).
function go_module_dirs() {
  source_files '*go.mod' | while IFS= read -r mod; do dirname "${mod}"; done
}

function check_modules() {
  ensure_staticcheck
  local dir
  local failed=0
  while IFS= read -r dir; do
    log "🔍 go vet + staticcheck: ${dir}"
    (cd "${dir}" && go vet ./...) || failed=1
    (cd "${dir}" && staticcheck ./...) || failed=1
  done < <(go_module_dirs)
  if [ "${failed}" -ne 0 ]; then
    log '❌ go vet / staticcheck reported findings (fix them in source; rules turn off only in linter configuration)'
    exit 1
  fi
  log '✅ go vet + staticcheck: clean'
}

main "${@:-}"
