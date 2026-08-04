#!/usr/bin/env bash
# coverage-go.sh
# Emit Go coverage as an LCOV tracefile for EVERY Go module in the repo — the
# root module, or nested modules (clients/*, proxy/*) when there is no root
# go.mod. LCOV is the common interchange format (same as lcov/gcov on Linux), so
# `make coverage` can concatenate this with the other languages' tracefiles into
# one report bucketed by source-file extension. Skips with exit 0 when there is
# no Go scope.
#
# Output: ${COVERAGE_DIR:-target/coverage}/go.lcov  (SF: entries are repo-relative
# filesystem paths, so they bucket by extension in the aggregate.)

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/paths.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"

readonly COVERAGE_DIR="${COVERAGE_DIR:-target/coverage}"
readonly GO_LCOV="${COVERAGE_DIR}/go.lcov"
# Entry points (cmd/*/main.go) are exercised only via the built binary and its
# BATS/integration tests, which are not coverage-instrumented — excluded by
# default so the per-file rule stays on library code, mirroring the Rust scope's
# src/bin/ exclusion. Override with GO_COVERAGE_IGNORE_REGEX.
readonly GO_COVERAGE_IGNORE_REGEX="${GO_COVERAGE_IGNORE_REGEX:-/cmd/}"

function log() {
  printf '%s\n' "${1:-}" | tee -a '/tmp/keel_coverage_go.log' >&5
}

function main() {
  exec 5>&1
  local dirs
  dirs="$(go_module_dirs)"
  if [ -z "${dirs}" ]; then
    log 'ℹ️  No Go scope detected (no go.mod); skipping coverage-go.'
    return 0
  fi
  mkdir -p "${COVERAGE_DIR}"
  local combined
  combined="$(mktemp)"
  printf '%s\n' "${dirs}" | while IFS= read -r dir; do
    [ -n "${dir}" ] && collect_module "${dir}"
  done > "${combined}"
  grep -Ev "${GO_COVERAGE_IGNORE_REGEX}" "${combined}" | to_lcov > "${GO_LCOV}"
  rm -f "${combined}"
  log "📊 Go LCOV -> ${GO_LCOV} ($(grep -c '^SF:' "${GO_LCOV}" || echo 0) files)"
}

# go_module_dirs: repo-relative dirs containing a go.mod (root or nested).
# `git ls-files` makes discovery layout-agnostic and naturally excludes git
# submodules (tracked as gitlinks, not their files) and vendored deps (no go.mod),
# rather than hardcoding one project's module layout. filter_src is retained as a
# belt-and-suspenders drop of any stray tracked vendor go.mod.
function go_module_dirs() {
  if [ -f 'go.mod' ]; then
    echo '.'
    return 0
  fi
  git ls-files '*go.mod' 2>/dev/null \
    | while IFS= read -r m; do dirname "${m}"; done \
    | filter_src \
    | sort
}

# collect_module: run coverage in one module and rewrite its import-path-prefixed
# coverprofile lines to repo-relative filesystem paths. Modules without tests
# produce no profile and are skipped.
function collect_module() {
  local dir="${1}" prof modpath prefix
  prof="$(mktemp)"
  ( cd "${dir}" && go test -covermode=atomic -coverprofile="${prof}" ./... >/dev/null 2>&1 ) || true
  if [ ! -s "${prof}" ]; then
    rm -f "${prof}"
    return 0
  fi
  # GOWORK=off so `go list -m` returns THIS module's path only; in a go.work
  # workspace it otherwise lists every module (multi-line), corrupting the sed below.
  modpath="$(cd "${dir}" && GOWORK=off go list -m 2>/dev/null)"
  if [ "${dir}" = '.' ]; then prefix=''; else prefix="${dir}/"; fi
  grep -v '^mode:' "${prof}" | sed "s|^${modpath}/|${prefix}|"
  rm -f "${prof}"
}

# to_lcov: convert a Go coverprofile (filesystem paths) on stdin into an LCOV
# tracefile. A covered block marks its whole line span; a line's hit count is the
# max over the blocks covering it — equivalent to jandelgado/gcov2lcov.
function to_lcov() {
  awk '
    {
      split($1, a, ":"); path = a[1]
      split(a[2], b, ","); split(b[1], s, "."); split(b[2], e, ".")
      cnt = $3
      for (ln = s[1]; ln <= e[1]; ln++) {
        key = path SUBSEP ln
        if (!(key in seen) || cnt + 0 > hit[key] + 0) hit[key] = cnt
        seen[key] = 1
        if (ln > mx[path]) mx[path] = ln
        if (!(path in F)) { F[path] = 1; ord[++n] = path }
      }
    }
    END {
      for (i = 1; i <= n; i++) {
        f = ord[i]; print "SF:" f; lf = 0; lh = 0
        for (ln = 1; ln <= mx[f]; ln++) {
          key = f SUBSEP ln
          if (key in seen) { print "DA:" ln "," hit[key]; lf++; if (hit[key] + 0 > 0) lh++ }
        }
        print "LF:" lf; print "LH:" lh; print "end_of_record"
      }
    }
  '
}

main "${@:-}"
