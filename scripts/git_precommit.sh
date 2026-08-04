#!/usr/bin/env bash
# scripts/git_precommit.sh
# Pre-commit hook entry point. Runs:
#   1. format.sh  — auto-fix what's auto-fixable
#   2. lint.sh    — fail on remaining lint issues
#   3. governance-gate.sh — block commit on governance drift (canonical
#      script staleness, missing canonical Makefile targets, OR recipe
#      drift between consumer Makefile and
#      .standards/templates/Makefile.canonical for shared target names).
#      Per user directive 2026-05-24: pre-commit MUST catch consumer-side
#      recipe changes to a canonical target that aren't reflected in the
#      templates version.
# Install: ln -sf ../../scripts/git_precommit.sh .git/hooks/pre-commit

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# Restore Homebrew / system-wide PATH additions that interactive-shell init
# files normally provide. `bash scripts/X.sh` does NOT source ~/.bashrc or
# ~/.zshrc, so without this every downstream tool (shellcheck, node,
# markdownlint-cli2, syft, ...) looks "missing" when it isn't.
# shellcheck source=lib/paths-ensure.sh
. "$(dirname "$(readlink -f "${BASH_SOURCE[0]}" 2>/dev/null || echo "${BASH_SOURCE[0]}")")/lib/paths-ensure.sh"
paths_ensure_standard

function main() {
  exec 5>&1
  log 'Pre-commit: format + lint + governance-gate'
  bash scripts/format.sh
  bash scripts/lint.sh
  bash scripts/ci/governance-gate.sh
  log '✅ Pre-commit checks passed'
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/git-precommit.log' >&5
}

main "${@:-}"
