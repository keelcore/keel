#!/usr/bin/env bash
# scripts/lint/forbid-lint-suppressions.sh
# Fail if any repo-owned source file contains a lint-suppression directive
# (shellcheck disable, markdownlint disable/capture). Per feedback_never_bypass_lint:
# refactor code so the warning no longer applies; never silence the linter.
#
# Allowed (NOT a disable — just a hint that tells the linter where to look):
#   # shellcheck source=<path>     (e.g., source=./lib/common.sh)
#
# Scope: a suppression directive can only live in the file type that carries it, so
# each pattern is scanned ONLY over its file type — via the vendoring-aware
# source_files generator (scripts/lib/paths.sh) and a SINGLE `git grep`. Never a
# per-file grep over the whole tree, and never vendored / submodule content.

set -o nounset
set -o errexit
set -o pipefail

declare REPO_ROOT
REPO_ROOT="$(git rev-parse --show-toplevel)"

declare SELF_REL
SELF_REL="$(realpath "${BASH_SOURCE[0]}")"
SELF_REL="${SELF_REL#"${REPO_ROOT}"/}"

# shellcheck source=../lib/paths.sh
. "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"

function main() {
  exec 5>&1
  local rc=0
  # Suppression directives are type-bound: a shell "disable=" comment can only appear in
  # shell, and markdownlint disable/capture only in markdown. Scan each over its own type.
  scan 'shellcheck +disable='                'shellcheck disable='          '*.sh' '*.bash' || rc=1
  scan '<!-- *markdownlint-(disable|capture)' 'markdownlint-disable/capture' '*.md' '*.markdown' || rc=1
  if [ "${rc}" -eq 0 ]; then
    log '✅ no lint-suppression directives in tracked files'
  fi
  exit "${rc}"
}

# scan PATTERN LABEL GLOB...: report any repo-owned file of the given globs whose
# content matches PATTERN. source_files excludes vendored/submodule trees by
# construction; a single `git grep` over the resulting (small) file set does the
# content search; this script excludes itself (it necessarily quotes the patterns).
function scan() {
  local -r pat="${1}" label="${2}"
  shift 2
  local files matches
  files="$(source_files "$@" | grep -vxF "${SELF_REL}" || true)"
  [ -z "${files}" ] && return 0
  # NUL-delimit the (small, type-scoped) file list into a SINGLE `git grep`.
  matches="$(printf '%s\n' "${files}" | tr '\n' '\0' \
             | xargs -0 git -C "${REPO_ROOT}" grep -lE "${pat}" -- 2>/dev/null || true)"
  [ -z "${matches}" ] && return 0
  log "❌ forbidden suppression (${label}):"
  printf '%s\n' "${matches}" | sed 's/^/   /' >&5
  log '   → refactor to fix the lint issue at the source (feedback_never_bypass_lint)'
  return 1
}

function log() {
  printf '%s\n' "${1:-}" >&5
}

main "${@:-}"
