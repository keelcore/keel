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