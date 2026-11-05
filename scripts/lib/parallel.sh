#!/usr/bin/env bash
# parallel.sh
# Run one function over many items at once. Source this file; do not execute directly.

# run_parallel FN ITEM... [-- ARG...]: runs `FN ITEM ARG...` concurrently, one job per item, and
# waits for all of them: a failure elsewhere never interrupts a job, so a retried release finds
# each job finished or cleanly failed. Returns non-zero if any job failed.
#
# Each job's stdout, stderr and fd 5 are held back, then printed whole, in the order given, so logs
# from concurrent jobs stay readable. A job reports its results through files, never stdout. The
# caller has fd 5 open and has errexit on; each job runs with errexit on, as long as the call itself
# is a plain command (not in `if`, `&&`, `||` or `$(...)`).
function run_parallel() {
  local -r fn="${1}"
  shift
  local -a items=()
  while [ "${#}" -gt 0 ] && [ "${1}" != '--' ]; do
    items+=("${1}")
    shift
  done
  [ "${#}" -eq 0 ] || shift
  local logs i
  logs="$(mktemp -d)"
  local -a pids=()
  for i in "${!items[@]}"; do
    ("${fn}" "${items[${i}]}" "$@") > "${logs}/${i}" 2>&1 5>&1 &
    pids+=("${!}")
  done
  local failed=0
  for i in "${!items[@]}"; do
    if ! wait "${pids[${i}]}"; then
      failed=1
      printf '❌ %s %s failed\n' "${fn}" "${items[${i}]}" >> "${logs}/${i}"
    fi
    cat "${logs}/${i}" >&5
  done
  rm -rf "${logs}"
  return "${failed}"
}
