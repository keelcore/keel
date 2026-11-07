#!/usr/bin/env bash
# scripts/lib/progress.sh
# Progress lines for long runs whose output goes to a log: the latest image build step and log line.
# Shared by scripts/release/update-pins.sh and scripts/lib/dev-image.sh. Source this file.

# progress_latest LOG: "step N/M: <latest line>" of LOG, without the build's step number and timer,
# or "starting…" before it has a line.
function progress_latest() {
  local step latest
  step="$(sed -nE 's/^#[0-9]+ \[.* ([0-9]+\/[0-9]+)\].*/\1/p' "${1}" 2> /dev/null | tail -n 1)"
  latest="$(awk 'NF { line = $0 } END { print line }' "${1}" 2> /dev/null | sed -E 's/^#[0-9]+ [0-9.]+ //' | cut -c1-100)"
  printf '%s%s' "${step:+step ${step}: }" "${latest:-starting…}"
}

# elapsed_mmss SECONDS: SECONDS as 12m05s.
function elapsed_mmss() {
  printf '%dm%02ds' "$(( ${1} / 60 ))" "$(( ${1} % 60 ))"
}
