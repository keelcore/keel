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

# wait_for_tcp <host> <port> [timeout-seconds=10]
# Waits until <host>:<port> accepts a TCP connection. Sends no data, so it is
# safe for fake upstreams (nc) that capture whatever they receive. For servers
# that do not log listener_up.
wait_for_tcp() {
  local -r host="${1}"
  local -r port="${2}"
  local -r timeout="${3:-10}"
  local i
  for (( i = 0; i < timeout * 20; i++ )); do
    ( exec 3<>"/dev/tcp/${host}/${port}" ) 2>/dev/null && return 0
    sleep 0.05
  done
  printf 'wait_for_tcp: %s:%s not accepting connections after %ss\n' "${host}" "${port}" "${timeout}" >&2
  return 1
}
