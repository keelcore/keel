#!/usr/bin/env bats

# bash configuration per project discipline
set -o nounset
set -o errexit
set -o pipefail

# ---------------------------------------------------------------------------
# Setup: put dist/ on PATH so tests can invoke keel-min / keel-fips directly.
# ---------------------------------------------------------------------------

setup() {
  PATH="${BATS_TEST_DIRNAME}/../dist:${PATH}"
  export PATH
}

# ---------------------------------------------------------------------------
# Helper: write a minimal all-listeners-disabled config to a temp file.
# Returns the file path on stdout.
# ---------------------------------------------------------------------------

minimal_config() {
  local f
  f="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\n' > "${f}"
  printf '%s' "${f}"
}

# ---------------------------------------------------------------------------
# Binary size
# ---------------------------------------------------------------------------

@test "keel-min is under 8 MB (Linux CI gate)" {
  # macOS Darwin binaries include extra Mach-O sections; gate applies to Linux.
  [[ "$(uname)" == "Linux" ]] || skip "size gate applies to Linux CI only"
  local size
  size="$(wc -c < ./dist/keel-min | tr -d '[:space:]')"
  [ "${size}" -lt 8388608 ]
}

# ---------------------------------------------------------------------------
# CLI flags
# ---------------------------------------------------------------------------

@test "--version exits 0 and prints version" {
  run keel-min --version
  [ "${status}" -eq 0 ]
  [[ "${output}" =~ "keel" ]]
}

@test "--validate with default config exits 0" {
  run keel-min --validate
  [ "${status}" -eq 0 ]
  [[ "${output}" =~ "config ok" ]]
}

@test "--validate with invalid config exits non-zero" {
  local bad_config
  bad_config="$(mktemp)"
  printf 'listeners:\n  https:\n    enabled: true\n' > "${bad_config}"
  run env KEEL_CONFIG="${bad_config}" keel-min --validate
  rm -f "${bad_config}"
  [ "${status}" -ne 0 ]
}

@test "--help exits 0 and prints usage" {
  run keel-min --help
  [ "${status}" -eq 0 ]
  [[ "${output}" =~ "Usage" ]]
}

# ---------------------------------------------------------------------------
# Signal handling
# ---------------------------------------------------------------------------

@test "SIGTERM causes clean shutdown (exit 0)" {
  local config_file pid
  config_file="$(minimal_config)"
  KEEL_CONFIG="${config_file}" keel-min &
  pid="${!}"
  sleep 0.3
  kill -TERM "${pid}"
  wait "${pid}"
  local exit_code="${?}"
  rm -f "${config_file}"
  [ "${exit_code}" -eq 0 ]
}

@test "SIGHUP reloads without crashing; SIGTERM then exits 0" {
  local config_file pid
  config_file="$(minimal_config)"
  KEEL_CONFIG="${config_file}" keel-min &
  pid="${!}"
  sleep 0.3
  kill -HUP "${pid}"
  sleep 0.2
  # Process must still be alive after SIGHUP.
  kill -0 "${pid}"
  kill -TERM "${pid}"
  wait "${pid}"
  local exit_code="${?}"
  rm -f "${config_file}"
  [ "${exit_code}" -eq 0 ]
}

# ---------------------------------------------------------------------------
# Runtime port sanity
# ---------------------------------------------------------------------------

cert_dir() {
  printf '%s' "${BATS_TEST_DIRNAME}/fixtures/certs"
}

@test "HTTP listener: GET / returns 'keel: ok'" {
  local cfg pid
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: true\n  health:\n    enabled: false\n  ready:\n    enabled: false\nauthn:\n  enabled: false\n' > "${cfg}"
  KEEL_CONFIG="${cfg}" keel-min &
  pid="${!}"
  sleep 0.4
  local body
  body="$(curl -s --max-time 2 http://127.0.0.1:8080/)"
  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}"
  [[ "${body}" =~ "keel: ok" ]]
}

@test "health listener: GET /healthz returns 'ok'" {
  local cfg pid
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  health:\n    enabled: true\n  ready:\n    enabled: false\n' > "${cfg}"
  KEEL_CONFIG="${cfg}" keel-min &
  pid="${!}"
  sleep 0.4
  local body
  body="$(curl -s --max-time 2 http://127.0.0.1:9091/healthz)"
  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}"
  [[ "${body}" =~ "ok" ]]
}

@test "ready listener: GET /readyz returns 'ready'" {
  local cfg pid
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: true\n' > "${cfg}"
  KEEL_CONFIG="${cfg}" keel-min &
  pid="${!}"
  sleep 0.4
  local body
  body="$(curl -s --max-time 2 http://127.0.0.1:9092/readyz)"
  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}"
  [[ "${body}" =~ "ready" ]]
}

@test "HTTPS listener: GET / returns 'keel: ok'" {
  local cert_dir
  cert_dir="$(cert_dir)"
  [ -f "${cert_dir}/server.crt" ] || skip "TLS certs not found; run 'make gen-certs' first"
  local cfg pid
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  https:\n    enabled: true\n  health:\n    enabled: false\n  ready:\n    enabled: false\nauthn:\n  enabled: false\ntls:\n  cert_file: %s\n  key_file: %s\n' \
    "${cert_dir}/server.crt" "${cert_dir}/server.key" > "${cfg}"
  KEEL_CONFIG="${cfg}" keel-min &
  pid="${!}"
  sleep 0.4
  local body
  body="$(curl -s --max-time 2 --cacert "${cert_dir}/ca.crt" https://127.0.0.1:8443/)"
  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}"
  [[ "${body}" =~ "keel: ok" ]]
}

# ---------------------------------------------------------------------------
# Request ID and trace context headers
# ---------------------------------------------------------------------------

@test "HTTP response includes X-Request-ID header" {
  local cfg pid
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: true\n  health:\n    enabled: false\n  ready:\n    enabled: false\nauthn:\n  enabled: false\n' > "${cfg}"
  KEEL_CONFIG="${cfg}" keel-min &
  pid="${!}"
  sleep 0.4
  local headers
  headers="$(curl -sI --max-time 2 http://127.0.0.1:8080/ 2>&1)"
  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}"
  echo "${headers}" | grep -qi "x-request-id"
}

@test "HTTP response includes traceparent header" {
  local cfg pid
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: true\n  health:\n    enabled: false\n  ready:\n    enabled: false\nauthn:\n  enabled: false\n' > "${cfg}"
  KEEL_CONFIG="${cfg}" keel-min &
  pid="${!}"
  sleep 0.4
  local headers
  headers="$(curl -sI --max-time 2 http://127.0.0.1:8080/ 2>&1)"
  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}"
  echo "${headers}" | grep -qi "traceparent"
}

# ---------------------------------------------------------------------------
# Admin port endpoints
# ---------------------------------------------------------------------------

@test "admin /version returns 200 with JSON version field" {
  local cfg pid
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\n  admin:\n    enabled: true\n' > "${cfg}"
  KEEL_CONFIG="${cfg}" keel-min &
  pid="${!}"
  sleep 0.4
  local body
  body="$(curl -s --max-time 2 http://127.0.0.1:9999/version)"
  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}"
  echo "${body}" | grep -q '"version"'
}

@test "admin /debug/pprof/ returns 200" {
  local cfg pid
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\n  admin:\n    enabled: true\n' > "${cfg}"
  KEEL_CONFIG="${cfg}" keel-min &
  pid="${!}"
  sleep 0.4
  local status
  status="$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 http://127.0.0.1:9999/debug/pprof/)"
  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}"
  [ "${status}" -eq 200 ]
}

@test "POST /admin/reload returns 200" {
  local cfg pid
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\n  admin:\n    enabled: true\n' > "${cfg}"
  KEEL_CONFIG="${cfg}" keel-min &
  pid="${!}"
  sleep 0.4
  local status
  status="$(curl -s -o /dev/null -w '%{http_code}' -X POST --max-time 2 http://127.0.0.1:9999/admin/reload)"
  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}"
  [ "${status}" -eq 200 ]
}

# ---------------------------------------------------------------------------
# FIPS build (skipped when keel-fips binary is absent)
# ---------------------------------------------------------------------------

@test "keel-fips fails-closed in non-FIPS environment" {
  command -v keel-fips > /dev/null 2>&1 || skip "keel-fips binary not in dist/"
  export FIPS_ENABLED=true
  run keel-fips
  [ "${status}" -eq 1 ]
  [[ "${output}" =~ "Failing closed" ]]
}