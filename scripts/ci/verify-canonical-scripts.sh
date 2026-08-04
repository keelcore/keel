#!/usr/bin/env bash
# verify-canonical-scripts.sh
# Verifies that canonical CI scripts in a downstream repo are byte-identical
# to the source-of-truth copies in this standards repository.
#
# Usage: verify-canonical-scripts.sh <downstream-repo-root>
#
# For each mandatory canonical script the script compares sha256sum of:
#   <standards-repo>/scripts/X  vs  <downstream-repo>/scripts/X
# and reports PASS/FAIL per file. Exits 1 if any mismatch is found.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly LOG_FILE='/tmp/verify_canonical.log'

# Canonical scripts that must be byte-identical in every downstream repo.
# Paths are relative to each repo's root (scripts/...).
# Excludes project-specific scripts (pr-policy.sh, check-legal-drift.sh)
# whose content must be tailored per project.
readonly -a CANONICAL_SCRIPTS=(
  'scripts/ci/audit-make-targets.sh'
  'scripts/ci/governance-gate.sh'
  'scripts/ci/secret-scan.sh'
  'scripts/ci/dco-check.sh'
  'scripts/ci/setup-bats.sh'
  'scripts/ci/setup-markdownlint.sh'
  'scripts/ci/setup-shellcheck.sh'
  'scripts/ci/setup-syft.sh'
  'scripts/install-hooks.sh'
  'scripts/format.sh'
  'scripts/lint.sh'
  'scripts/git_precommit.sh'
  'scripts/lint/markdown.sh'
  'scripts/lint/newlines.sh'
  'scripts/lint/shellcheck.sh'
  'scripts/test/coverage-delta.sh'
  'scripts/test/coverage-go.sh'
  'scripts/test/coverage-rust.sh'
  'scripts/test/coverage-no-regression.sh'
  'scripts/test/coverage-baseline-init.sh'
  'scripts/lib/paths.sh'
  'scripts/check/adr-metadata.sh'
  'scripts/check/governance-metadata.sh'
  'scripts/check/rfc-metadata.sh'
)

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a "${LOG_FILE}"
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    if [ "${#}" -eq 0 ] || [ -z "${1:-}" ]; then
      log '❌ Error: downstream repo root path is required'
      log 'Usage: verify-canonical-scripts.sh <downstream-repo-root>'
      exit 1
    fi
  fi
  if [ ! -d "${1}" ]; then
    log "❌ Error: downstream repo root not found: ${1}"
    exit 1
  fi
}

function sha256_of() {
  local -r file="${1}"
  sha256sum "${file}" | awk '{print $1}'
}

function verify_script() {
  local -r standards_root="${1}"
  local -r downstream_root="${2}"
  local -r rel_path="${3}"

  local -r standards_file="${standards_root}/${rel_path}"
  local -r downstream_file="${downstream_root}/${rel_path}"

  if [ ! -f "${standards_file}" ]; then
    log "  ⚠️  SKIP  ${rel_path} (not found in standards repo)"
    return 0
  fi

  if [ ! -f "${downstream_file}" ]; then
    log "  ❌ FAIL  ${rel_path} (missing in downstream repo)"
    return 1
  fi

  local standards_sum downstream_sum
  standards_sum="$(sha256_of "${standards_file}")"
  downstream_sum="$(sha256_of "${downstream_file}")"

  if [ "${standards_sum}" = "${downstream_sum}" ]; then
    log "  ✅ PASS  ${rel_path}"
    return 0
  fi

  log "  ❌ FAIL  ${rel_path}"
  log "     standards : ${standards_sum}"
  log "     downstream: ${downstream_sum}"
  return 1
}

function main() {
  validate_args "${@:-}"
  local -r downstream_root="${1}"
  local standards_root
  standards_root="$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)"

  log "Verifying canonical scripts against: ${downstream_root}"
  log "Standards source of truth: ${standards_root}"
  log ""

  local failed=0
  for script in "${CANONICAL_SCRIPTS[@]}"; do
    verify_script "${standards_root}" "${downstream_root}" "${script}" || failed=1
  done

  log ""
  if [ "${failed}" -eq 1 ]; then
    log "❌ One or more canonical scripts are out of sync."
    log "   Copy the source-of-truth versions from the standards repo and re-commit."
    exit 1
  fi

  log "✅ All canonical scripts match the standards source of truth."
}

main "${@:-}"
