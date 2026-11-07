# tests/helpers/wait.bash
# Synchronization helpers for BATS: wait for a real readiness signal instead of
# a fixed sleep. Load with `load helpers/wait`.
#
# keel's logger writes each line with one unbuffered write to stdout, and logs
# listener_up after the socket is bound and before Serve — so once the line is
# in the log file, connections to that port are accepted. No flush delay.

# wait_for_log <file> <ERE> [timeout-seconds=10]
# Polls <file> every 50 ms for a line matching <ERE>. On timeout, prints the
# file's last lines to stderr and fails.
wait_for_log() {
  local -r file="${1}"
  local -r pattern="${2}"
  local -r timeout="${3:-10}"
  local i
  for (( i = 0; i < timeout * 20; i++ )); do
    grep -qE -- "${pattern}" "${file}" 2>/dev/null && return 0
    sleep 0.05
  done
  printf 'wait_for_log: no line matching /%s/ in %s after %ss; last lines:\n' "${pattern}" "${file}" "${timeout}" >&2
  tail -n 20 "${file}" >&2 2>/dev/null || true
  return 1
}

# wait_for_keel_listener <logfile> <port> [timeout-seconds=10]
# Waits for keel's listener_up event for <port> in its JSON log.
wait_for_keel_listener() {
  local -r log="${1}"
  local -r port="${2}"
  local -r timeout="${3:-10}"
  wait_for_log "${log}" "\"addr\":\"[^\"]*:${port}\".*\"msg\":\"listener_up\"" "${timeout}"
}

# run_bounded SECONDS CMD...: run CMD, stopping it at the limit. CMD's status, or 124 if it hit the
# limit. The watchdog holds none of the caller's output, so `run` returns as soon as CMD ends.
run_bounded() {
  local -r limit="${1}"
  shift
  local flag
  flag="$(mktemp)"
  rm -f "${flag}"
  "$@" &
  local -r pid="${!}"
  ( sleep "${limit}"; : > "${flag}"; kill "${pid}" 2> /dev/null ) > /dev/null 2>&1 &
  local -r watchdog="${!}"
  local status=0
  wait "${pid}" || status="${?}"
  kill "${watchdog}" 2> /dev/null || true
  wait "${watchdog}" 2> /dev/null || true
  if [ -e "${flag}" ]; then
    rm -f "${flag}"
    return 124
  fi
  return "${status}"
}
