#!/usr/bin/env bats

# bash configuration per project discipline
set -o nounset
set -o errexit
set -o pipefail

# ---------------------------------------------------------------------------
# Setup: put dist/ on PATH so tests can invoke myapp directly.
# ---------------------------------------------------------------------------

setup() {
  PATH="${BATS_TEST_DIRNAME}/../../dist:${PATH}"
  export PATH
}

# ---------------------------------------------------------------------------
# Helper: write a minimal all-listeners-disabled config to a temp file.
# ---------------------------------------------------------------------------

minimal_config() {
  local f
  f="$(mktemp)"
  printf 'app:\n  name: myapp\nkeel:\n  listeners:\n    http:\n      enabled: false\n    health:\n      enabled: false\n    ready:\n      enabled: false\n' > "${f}"
  printf '%s' "${f}"
}

# ---------------------------------------------------------------------------
# Flag tests
# ---------------------------------------------------------------------------

@test "--version exits 0 and prints version" {
  run myapp --version
  [ "${status}" -eq 0 ]
  [[ "${output}" =~ "keel" ]]
}

@test "--validate with valid config exits 0" {
  local cfg
  cfg="$(minimal_config)"
  run env APP_CONFIG="${cfg}" myapp --validate
  rm -f "${cfg}"
  [ "${status}" -eq 0 ]
  [[ "${output}" =~ "config ok" ]]
}

@test "--validate with invalid config exits non-zero" {
  local bad
  bad="$(mktemp)"
  printf 'keel:\n  listeners:\n    https:\n      enabled: true\n' > "${bad}"
  run env APP_CONFIG="${bad}" myapp --validate
  rm -f "${bad}"
  [ "${status}" -ne 0 ]
}

# ---------------------------------------------------------------------------
# Runtime test
# ---------------------------------------------------------------------------

@test "GET /hello returns expected body" {
  local cert_dir
  cert_dir="${BATS_TEST_DIRNAME}/../../tests/fixtures/certs"
  if [ ! -f "${cert_dir}/server.crt" ]; then
    "${BATS_TEST_DIRNAME}/../../tests/fixtures/gen-certs.sh"
  fi

  local cfg pid
  cfg="$(mktemp)"
  printf 'app:\n  name: myapp\nkeel:\n  listeners:\n    http:\n      enabled: false\n    https:\n      enabled: true\n    health:\n      enabled: false\n    ready:\n      enabled: false\n  tls:\n    cert_file: %s\n    key_file: %s\n  authn:\n    enabled: false\n' \
    "${cert_dir}/server.crt" "${cert_dir}/server.key" > "${cfg}"

  APP_CONFIG="${cfg}" myapp &
  pid="${!}"
  sleep 0.4

  local body
  body="$(curl -s --max-time 2 --cacert "${cert_dir}/ca.crt" "https://127.0.0.1:8443/hello")"
  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}"

  [[ "${body}" =~ "hello, from downstream app based on keel library" ]]
}

@test "health listener: GET /healthz returns 'ok'" {
  local cfg pid
  cfg="$(mktemp)"
  printf 'app:\n  name: myapp\nkeel:\n  listeners:\n    http:\n      enabled: false\n    https:\n      enabled: false\n    health:\n      enabled: true\n    ready:\n      enabled: false\n' > "${cfg}"
  APP_CONFIG="${cfg}" myapp &
  pid="${!}"
  sleep 0.4
  local body
  body="$(curl -s --max-time 2 http://127.0.0.1:9091/healthz)"
  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}"
  [[ "${body}" =~ "ok" ]]
}

@test "SIGTERM causes clean shutdown (exit 0)" {
  local cfg pid
  cfg="$(minimal_config)"
  APP_CONFIG="${cfg}" myapp &
  pid="${!}"
  sleep 0.3
  kill -TERM "${pid}"
  wait "${pid}"
  local exit_code="${?}"
  rm -f "${cfg}"
  [ "${exit_code}" -eq 0 ]
}

# ---------------------------------------------------------------------------
# /readyz gates on an embedder listener registered via RegisterListener.
# MYAPP_CLIENT_LISTENER_ADDR / MYAPP_CLIENT_LISTENER_DELAY drive the client bind.
# ---------------------------------------------------------------------------

# ready_only_config: only the ready listener (:9092) enabled.
ready_only_config() {
  local f
  f="$(mktemp)"
  printf 'app:\n  name: myapp\nkeel:\n  listeners:\n    http:\n      enabled: false\n    https:\n      enabled: false\n    health:\n      enabled: false\n    ready:\n      enabled: true\n' > "${f}"
  printf '%s' "${f}"
}

# readyz_code: HTTP status from /readyz, or 000 if unreachable.
readyz_code() {
  curl -s -o /dev/null -w '%{http_code}' --max-time 1 http://127.0.0.1:9092/readyz || echo 000
}

# B1 — a slow client listener holds /readyz at 503 until it binds, then 200.
@test "readyz gates on a slow client listener: 503 during bind window, 200 after" {
  local cfg pid
  cfg="$(ready_only_config)"
  MYAPP_CLIENT_LISTENER_ADDR="127.0.0.1:19310" MYAPP_CLIENT_LISTENER_DELAY="3s" \
    APP_CONFIG="${cfg}" myapp &
  pid="${!}"
  # Wait for keel's ready listener to be serving (it returns 503 while the client
  # listener is still binding).
  sleep 0.6

  local during after
  during="$(readyz_code)"
  # Wait past the 3s client bind delay.
  sleep 3
  after="$(readyz_code)"

  kill -TERM "${pid}" 2>/dev/null || true
  wait "${pid}" 2>/dev/null || true
  rm -f "${cfg}"

  [ "${during}" = "503" ]
  [ "${after}" = "200" ]
}

# B2 — race poll: /readyz must be observed 503 before 200 and never flap back.
@test "readyz startup is monotonic: not-ready before ready, never flaps back" {
  local cfg pid
  cfg="$(ready_only_config)"
  MYAPP_CLIENT_LISTENER_ADDR="127.0.0.1:19311" MYAPP_CLIENT_LISTENER_DELAY="1500ms" \
    APP_CONFIG="${cfg}" myapp &
  pid="${!}"

  local i code observed_503=0 seen_200=0 flap=0
  for i in $(seq 1 40); do
    code="$(readyz_code)"
    if [ "${code}" = "503" ]; then
      observed_503=1
      [ "${seen_200}" = "1" ] && flap=1
    elif [ "${code}" = "200" ]; then
      seen_200=1
    fi
    sleep 0.1
  done

  kill -TERM "${pid}" 2>/dev/null || true
  wait "${pid}" 2>/dev/null || true
  rm -f "${cfg}"

  [ "${observed_503}" -eq 1 ]  # /readyz gated at some point during startup
  [ "${seen_200}" -eq 1 ]      # and eventually became ready
  [ "${flap}" -eq 0 ]          # and never flapped 200 -> 503
}

# B3 — a client listener that never binds keeps /readyz at 503 indefinitely
# (Q3: the barrier waits, it does not self-heal on a timeout).
@test "readyz stays not-ready while a client listener never binds" {
  local cfg pid
  cfg="$(ready_only_config)"
  MYAPP_CLIENT_LISTENER_ADDR="127.0.0.1:19312" MYAPP_CLIENT_LISTENER_DELAY="never" \
    APP_CONFIG="${cfg}" myapp &
  pid="${!}"
  sleep 0.6

  local c1 c2
  c1="$(readyz_code)"
  sleep 1.5
  c2="$(readyz_code)"

  kill -TERM "${pid}" 2>/dev/null || true
  wait "${pid}" 2>/dev/null || true
  rm -f "${cfg}"

  [ "${c1}" = "503" ]
  [ "${c2}" = "503" ]
}

# B4 — a failed listener bind surfaces as a non-zero process exit (Q3: no
# deadline path that masks the failure). A second instance contends for :9092.
@test "readyz: a failed listener bind exits non-zero (never silently ready)" {
  local cfg pid_a
  cfg="$(ready_only_config)"
  # Instance A holds the ready port.
  APP_CONFIG="${cfg}" myapp &
  pid_a="${!}"
  sleep 0.5

  # Instance B cannot bind :9092; it self-cancels after 1s, at which point the
  # buffered bind error drives a fatal, non-zero exit.
  run env KEEL_TEST_SHUTDOWN_AFTER=1s APP_CONFIG="${cfg}" myapp

  kill -TERM "${pid_a}" 2>/dev/null || true
  wait "${pid_a}" 2>/dev/null || true
  rm -f "${cfg}"

  [ "${status}" -ne 0 ]
}
