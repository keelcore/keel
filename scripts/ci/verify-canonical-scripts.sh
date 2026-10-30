#!/usr/bin/env bash
# verify-canonical-scripts.sh
# Verifies that canonical CI scripts in a downstream repo are byte-identical
# to the source-of-truth copies in this standards repository.
#
# Usage: verify-canonical-scripts.sh [<downstream-repo-root>]
#   (default: the current git repo; from a consumer, checks it against its
#   own .standards submodule)
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

# Rule A (bytewise identity): EVERY shell script shipped under the standards
# repo's scripts/ tree is canonical and must be byte-identical in the downstream
# repo. The list is derived from the standards tree, so the only script exempt
# from the check is one that exists in the downstream repo but NOT here (a
# project-local script) — it is never in the derived list. *.local.sh hooks are
# owned by the repo that holds them (the standards repo has its own), so they
# are never canonical and are excluded here.
function canonical_scripts() {
  local -r standards_root="${1}"
  find "${standards_root}/scripts" -name '*.sh' ! -name '*.local.sh' -type f \
    | sed "s|^${standards_root}/||" \
    | sort
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a "${LOG_FILE}"
}

function validate_args() {
  if [ "${#}" -gt 1 ]; then
    log '❌ Error: Unexpected argument'
    log 'Usage: verify-canonical-scripts.sh [<downstream-repo-root>]'
    exit 1
  fi
  if [ -n "${1:-}" ] && [ ! -d "${1}" ]; then
    log "❌ Error: downstream repo root not found: ${1}"
    exit 1
  fi
}

# resolve_standards_root: the source of truth. Run from the standards repo, it is
# that repo. Run from a consumer's own synced copy, the script's toplevel IS the
# consumer, so the source of truth is the consumer's .standards submodule.
function resolve_standards_root() {
  local -r downstream_root="${1}"
  local root
  root="$(cd "$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)" && pwd -P)"
  if [ "${root}" = "${downstream_root}" ] && [ -d "${downstream_root}/.standards/scripts" ]; then
    root="${downstream_root}/.standards"
  fi
  printf '%s\n' "${root}"
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
  local downstream_root standards_root
  downstream_root="$(cd "${1:-$(git rev-parse --show-toplevel)}" && pwd -P)"
  standards_root="$(resolve_standards_root "${downstream_root}")"

  log "Verifying canonical scripts against: ${downstream_root}"
  log "Standards source of truth: ${standards_root}"
  log ""

  local failed=0
  local script
  while IFS= read -r script; do
    verify_script "${standards_root}" "${downstream_root}" "${script}" || failed=1
  done < <(canonical_scripts "${standards_root}")

  log ""
  if [ "${failed}" -eq 1 ]; then
    log "❌ One or more canonical scripts are out of sync."
    log "   Copy the source-of-truth versions from the standards repo and re-commit."
    exit 1
  fi

  log "✅ All canonical scripts match the standards source of truth."
}

main "${@:-}"
