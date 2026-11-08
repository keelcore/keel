#!/usr/bin/env bash
# scripts/run-pre-commit.sh
# Pre-commit hook entry point. Runs:
#   0. update-dev-image-ref.sh — containers/NAME/image.ref names the image built from this commit.
#   1. format.sh  — auto-fix what's auto-fixable
#   2. lint-all.sh    — fail on remaining lint issues
#   3. check-governance.sh — block commit on governance drift (canonical
#      script staleness, missing canonical Makefile targets, OR recipe
#      drift between consumer Makefile and
#      .standards/templates/Makefile.canonical for shared target names).
#      Per user directive 2026-05-24: pre-commit MUST catch consumer-side
#      recipe changes to a canonical target that aren't reflected in the
#      templates version.
#   4. update-pins.sh --check-pins-doc — when the repository has docs/current_pins.md, the pins
#      it lists match the pins in the tree (local, no network): updated pins are committed with
#      their table.
# Install: ln -sf ../../scripts/run-pre-commit.sh .git/hooks/pre-commit
# Project hook: run-pre-commit.local.sh runs first, when present, with this script's arguments
# (scripts/lib/local-hook.sh: the same path, in the repository this runs in).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=./lib/local-hook.sh
source "$(dirname "${BASH_SOURCE[0]}")/./lib/local-hook.sh"

# Restore Homebrew / system-wide PATH additions that interactive-shell init
# files normally provide. `bash scripts/X.sh` does NOT source ~/.bashrc or
# ~/.zshrc, so without this every downstream tool (shellcheck, node,
# markdownlint-cli2, syft, ...) looks "missing" when it isn't.
# shellcheck source=lib/paths-ensure.sh
. "$(dirname "$(readlink -f "${BASH_SOURCE[0]}" 2>/dev/null || echo "${BASH_SOURCE[0]}")")/lib/paths-ensure.sh"
paths_ensure_standard

function main() {
  exec 5>&1
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  log 'Pre-commit: image name + format + lint + governance-gate + pins table'
  [ ! -f scripts/dev/update-dev-image-ref.sh ] || bash scripts/dev/update-dev-image-ref.sh
  bash scripts/dev/run.sh --writable bash scripts/format.sh
  bash scripts/dev/run.sh bash scripts/lint-all.sh
  bash scripts/dev/run.sh bash scripts/ci/check-governance.sh
  if [ -f docs/current_pins.md ]; then
    bash scripts/dev/run.sh bash scripts/release/update-pins.sh --check-pins-doc docs/current_pins.md
  fi
  log '✅ Pre-commit checks passed'
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/git-precommit.log' >&5
}

main "${@:-}"
