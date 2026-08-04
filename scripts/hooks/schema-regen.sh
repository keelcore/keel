#!/usr/bin/env bash
# scripts/hooks/schema-regen.sh
# keel-specific pre-commit step, composed onto the canonical `pre-commit` target
# via the `keel-schema-regen` Makefile prerequisite. When staged changes touch
# pkg/config/*.go, regenerate pkg/config/schema.yaml and stage it so the schema
# never drifts from the config structs. No-op when config is untouched.
#
# This is the one pre-commit check with no canonical equivalent (it regenerates
# and stages a keel-only artifact), so it lives here rather than in .standards.

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
  if [ -z "$(staged_config_go)" ]; then
    return 0
  fi
  log 'pkg/config/*.go changed — regenerating schema.yaml'
  bash scripts/release/gen-schema.sh
  if ! git diff --quiet pkg/config/schema.yaml; then
    git add pkg/config/schema.yaml
    log '✅ schema.yaml updated and staged'
  else
    log '✅ schema.yaml already up to date'
  fi
}

function staged_config_go() {
  git diff --cached --name-only --diff-filter=ACMR | grep '^pkg/config/.*\.go$' || true
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_schema_regen.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
