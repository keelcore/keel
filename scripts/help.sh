#!/usr/bin/env bash
# help.sh
# Auto-enumerate Make targets across one or more Makefiles. Invoked by the
# canonical `help` target as `bash scripts/help.sh $(MAKEFILE_LIST)` so the
# consumer sees both their own targets and the canonical ones inherited via
# include. Output is sorted and deduplicated.

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
  log 'Available targets:'
  log ''
  enumerate "${@:-}"
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/help.log' >&5
}

function validate_args() {
  : # variadic Makefile paths accepted; zero args produces no enumeration
}

function enumerate() {
  local file
  for file in "${@:-}"; do
    [ -z "${file:-}" ] && continue
    [ -f "${file}" ] || continue
    # Match `name:` at column 0 where the rest of the line is either empty
    # (target with no inline prereqs, e.g. `build:`) or begins with a non-`=`
    # character (target with prereqs, e.g. `lint: lint-bash`). Excludes
    # variable assignments like `VAR=x` and `target:=x`.
    awk '/^[a-zA-Z][a-zA-Z0-9_.-]*:($|[^=])/ {name=$0; sub(/:.*/, "", name); print "  " name}' "${file}"
  done | sort -u
}

main "${@:-}"
