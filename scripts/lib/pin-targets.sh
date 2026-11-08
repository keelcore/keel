#!/usr/bin/env bash
# scripts/lib/pin-targets.sh
# Make targets that verify a pin. Used by scripts/release/update-pins.sh. Source this file.

# setup_verify_targets SETUP: SETUP, then lint-<tool> when the project has that target: a scanner's pin is
# verified by installing it and scanning with it, so a release that changes findings fails before it applies.
function setup_verify_targets() {
  local -r setup="${1}" lint="lint-${1#setup-}"
  printf '%s' "${setup}"
  if make -n "${lint}" > /dev/null 2>&1; then
    printf ' %s' "${lint}"
  fi
}
