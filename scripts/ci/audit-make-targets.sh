#!/usr/bin/env bash
# audit-make-targets.sh
# CI/standards compliance auditor. Enforces three invariants:
#   1. Every workflow run: step is `make <target>` — no direct tool calls.
#   2. Every scripts/**/*.sh (except scripts/lib/) has a Makefile target.
#   3. Universal canonical targets (build, lint, test, unit-test,
#      integration-test, clean, audit) are defined in the Makefile.
#
# Exit 0 on full compliance; exit 1 with a summary of violations.
# Safe to run locally: make audit

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly REPO_ROOT="$(git rev-parse --show-toplevel)"
readonly MAKEFILE="${REPO_ROOT}/Makefile"
readonly WORKFLOWS_DIR="${REPO_ROOT}/.github/workflows"
readonly SCRIPTS_DIR="${REPO_ROOT}/scripts"

function log() {
  printf '%s\n' "${1:-}"
}

function validate_args() {
  if [ "${#}" -gt 0 ] && [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected arg'
    exit 1
  fi
}

# ---------------------------------------------------------------------------
# Rule 1: All workflow run: steps must be `make <target>`.
# ---------------------------------------------------------------------------
function check_workflow_run_steps() {
  local failed=0
  local violations

  # Match lines of the form "        run: <something>" where <something> is not "make".
  # Output from grep -rn includes "file:linenum:content" — the second grep matches
  # against the full line so we omit the ^ anchor from the exclusion pattern.
  violations="$(
    grep -rn '^\s*run:' "${WORKFLOWS_DIR}/" \
      | grep -v 'run:\s*make\s' \
      || true
  )"

  if [ -n "${violations}" ]; then
    log '❌ Rule 1: workflow run: steps must be `make <target>` — violations:'
    while IFS= read -r line; do
      log "  ${line}"
    done <<< "${violations}"
    failed=1
  fi

  return "${failed}"
}

# ---------------------------------------------------------------------------
# Rule 2: Every scripts/**/*.sh (except scripts/lib/) must appear in Makefile.
# ---------------------------------------------------------------------------
function check_script_targets() {
  local failed=0

  while IFS= read -r script_abs; do
    # Compute path relative to REPO_ROOT (strip leading path + /)
    local script_rel
    script_rel="${script_abs#"${REPO_ROOT}/"}"

    # Skip sourced library files — they are not standalone executables.
    if [[ "${script_rel}" == scripts/lib/* ]]; then
      continue
    fi

    # Check if the script path appears anywhere in the Makefile.
    if ! grep -qF "${script_rel}" "${MAKEFILE}"; then
      log "❌ Rule 2: no Makefile target invokes: ${script_rel}"
      failed=1
    fi
  done < <(find "${SCRIPTS_DIR}" -name '*.sh' | sort)

  return "${failed}"
}

# ---------------------------------------------------------------------------
# Rule 3: Universal canonical targets must exist.
# ---------------------------------------------------------------------------
readonly -a UNIVERSAL_TARGETS=(
  audit
  build
  clean
  integration-test
  lint
  test
  unit-test
)

function check_universal_targets() {
  local failed=0

  for target in "${UNIVERSAL_TARGETS[@]}"; do
    # A target is defined if Makefile contains a line starting with `<target>:`.
    if ! grep -qE "^${target}:" "${MAKEFILE}"; then
      log "❌ Rule 3: universal Makefile target missing: ${target}"
      failed=1
    fi
  done

  return "${failed}"
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
function main() {
  validate_args "${@:-}"

  local overall=0

  check_workflow_run_steps || overall=1
  check_script_targets     || overall=1
  check_universal_targets  || overall=1

  if [ "${overall}" -eq 0 ]; then
    log '✅ CI audit passed: all Makefile target invariants satisfied.'
  else
    log ''
    log 'Fix violations above, then re-run: make audit'
    exit 1
  fi
}

main "${@:-}"
