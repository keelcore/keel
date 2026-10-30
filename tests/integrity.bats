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
# Legal file drift
# ---------------------------------------------------------------------------

@test "LICENSE and TRADEMARK.md are identical to pkg/clisupport/ copies" {
  run bash "${BATS_TEST_DIRNAME}/../scripts/check-legal-drift.sh"
  [ "${status}" -eq 0 ]
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

@test "HTTPS negotiates HTTP/2 via ALPN (keel-max); keel-min (no_h2) stays on HTTP/1.1" {
  command -v keel-max > /dev/null 2>&1 || skip "keel-max binary not in dist/"
  local cert_dir
  cert_dir="$(cert_dir)"
  [ -f "${cert_dir}/server.crt" ] || skip "TLS certs not found; run 'make gen-certs' first"
  local cfg pid bin max_ver min_ver
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  https:\n    enabled: true\n  h3:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\nauthn:\n  enabled: false\ntls:\n  cert_file: %s\n  key_file: %s\n' \
    "${cert_dir}/server.crt" "${cert_dir}/server.key" > "${cfg}"
  for bin in keel-max keel-min; do
    KEEL_CONFIG="${cfg}" "${bin}" > /dev/null 2>&1 &
    pid="${!}"
    local _w=0
    until curl -s -o /dev/null --max-time 1 --cacert "${cert_dir}/ca.crt" https://127.0.0.1:8443/ || [ "${_w}" -ge 30 ]; do
      sleep 0.1; _w=$(( _w + 1 ))
    done
    if [ "${bin}" = 'keel-max' ]; then
      max_ver="$(curl -s --http2 -o /dev/null -w '%{http_version}' --max-time 2 --cacert "${cert_dir}/ca.crt" https://127.0.0.1:8443/)"
    else
      min_ver="$(curl -s --http2 -o /dev/null -w '%{http_version}' --max-time 2 --cacert "${cert_dir}/ca.crt" https://127.0.0.1:8443/)"
    fi
    kill -TERM "${pid}"
    wait "${pid}" || true
  done
  rm -f "${cfg}"
  [ "${max_ver}" = '2' ]
  [ "${min_ver}" = '1.1' ]
}

@test "tls.client_ca_file: HTTPS rejects clients without a trusted certificate (inbound mTLS)" {
  local cert_dir
  cert_dir="$(cert_dir)"
  [ -f "${cert_dir}/client.crt" ] || skip "TLS certs not found; run 'make gen-certs' first"
  local cfg pid
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  https:\n    enabled: true\n  health:\n    enabled: false\n  ready:\n    enabled: false\nauthn:\n  enabled: false\ntls:\n  cert_file: %s\n  key_file: %s\n  client_ca_file: %s\n' \
    "${cert_dir}/server.crt" "${cert_dir}/server.key" "${cert_dir}/ca.crt" > "${cfg}"
  KEEL_CONFIG="${cfg}" keel-min &
  pid="${!}"
  sleep 0.4
  local anon authed
  anon="$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 --cacert "${cert_dir}/ca.crt" https://127.0.0.1:8443/)" || true
  authed="$(curl -s --max-time 2 --cacert "${cert_dir}/ca.crt" \
    --cert "${cert_dir}/client.crt" --key "${cert_dir}/client.key" https://127.0.0.1:8443/)"
  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}"
  [ "${anon}" = "000" ]
  [[ "${authed}" =~ "keel: ok" ]]
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

@test "admin /debug/pprof/ requires admin.pprof and the admin token (keel-max)" {
  command -v keel-max > /dev/null 2>&1 || skip "keel-max binary not in dist/"
  local cfg pid
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\n  admin:\n    enabled: true\nadmin:\n  pprof: true\n' > "${cfg}"
  KEEL_CONFIG="${cfg}" KEEL_ADMIN_TOKEN='bats-admin-token' keel-max &
  pid="${!}"
  sleep 0.4
  local anon authed
  anon="$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 http://127.0.0.1:9999/debug/pprof/)"
  authed="$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 -H 'Authorization: Bearer bats-admin-token' http://127.0.0.1:9999/debug/pprof/)"
  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}"
  [ "${anon}" -eq 401 ]
  [ "${authed}" -eq 200 ]
}

@test "keel-min (no_pprof): admin.pprof has no effect, 404 and a pprof_not_compiled warning" {
  local cfg pid log status
  cfg="$(mktemp)"
  log="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\n  admin:\n    enabled: true\nadmin:\n  pprof: true\n' > "${cfg}"
  KEEL_CONFIG="${cfg}" KEEL_ADMIN_TOKEN='bats-admin-token' keel-min > "${log}" 2>&1 &
  pid="${!}"
  sleep 0.4
  status="$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 -H 'Authorization: Bearer bats-admin-token' http://127.0.0.1:9999/debug/pprof/)"
  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}"
  [ "${status}" -eq 404 ]
  grep -q 'pprof_not_compiled' "${log}"
  rm -f "${log}"
}

@test "POST /admin/reload requires the admin token" {
  local cfg pid
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\n  admin:\n    enabled: true\n' > "${cfg}"
  KEEL_CONFIG="${cfg}" KEEL_ADMIN_TOKEN='bats-admin-token' keel-min &
  pid="${!}"
  sleep 0.4
  local anon authed
  anon="$(curl -s -o /dev/null -w '%{http_code}' -X POST --max-time 2 http://127.0.0.1:9999/admin/reload)"
  authed="$(curl -s -o /dev/null -w '%{http_code}' -X POST --max-time 2 -H 'Authorization: Bearer bats-admin-token' http://127.0.0.1:9999/admin/reload)"
  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}"
  [ "${anon}" -eq 401 ]
  [ "${authed}" -eq 200 ]
}

# ---------------------------------------------------------------------------
# Prometheus metrics gate
# ---------------------------------------------------------------------------

@test "metrics.prometheus false: GET /metrics returns 404 (keel-max)" {
  command -v keel-max > /dev/null 2>&1 || skip "keel-max binary not in dist/"
  # macOS: poll until port 9999 is free (previous test may hold it briefly).
  if [[ "$(uname)" == "Darwin" ]]; then
    local _p=0
    while lsof -nP -iTCP:9999 -sTCP:LISTEN > /dev/null 2>&1; do
      sleep 0.1; _p=$(( _p + 1 ))
      [ "${_p}" -lt 50 ] || break
    done
  fi
  local cfg pid
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\n  admin:\n    enabled: true\nmetrics:\n  prometheus: false\n' > "${cfg}"
  KEEL_CONFIG="${cfg}" keel-max &
  pid="${!}"
  # macOS: keel-max (full build) cold-starts slowly; poll for admin port readiness.
  if [[ "$(uname)" == "Darwin" ]]; then
    local _q=0
    until curl -s --max-time 0.1 http://127.0.0.1:9999/ > /dev/null 2>&1 || [ "${_q}" -ge 30 ]; do
      sleep 0.1; _q=$(( _q + 1 ))
    done
  else
    sleep 0.4
  fi
  local status
  status="$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 http://127.0.0.1:9999/metrics)"
  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}"
  [ "${status}" -eq 404 ]
}

@test "metrics.prometheus true: GET /metrics returns 200 (keel-max)" {
  command -v keel-max > /dev/null 2>&1 || skip "keel-max binary not in dist/"
  local cfg pid
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\n  admin:\n    enabled: true\nmetrics:\n  prometheus: true\n' > "${cfg}"
  KEEL_CONFIG="${cfg}" keel-max &
  pid="${!}"
  sleep 0.4
  local status
  status="$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 http://127.0.0.1:9999/metrics)"
  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}"
  [ "${status}" -eq 200 ]
}

@test "tracing.otlp enabled: keel-max starts cleanly with no collector present (keel-max)" {
  command -v keel-max > /dev/null 2>&1 || skip "keel-max binary not in dist/"
  local cfg pid alive
  cfg="$(mktemp)"
  # otlptracehttp dials lazily: binary must start without a live collector.
  printf 'listeners:\n  http:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\ntracing:\n  otlp:\n    enabled: true\n    endpoint: "localhost:4318"\n    insecure: true\n' > "${cfg}"
  KEEL_CONFIG="${cfg}" keel-max &
  pid="${!}"
  sleep 0.4
  kill -0 "${pid}" 2>/dev/null
  alive="${?}"
  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}"
  [ "${alive}" -eq 0 ]
}

# ---------------------------------------------------------------------------
# Portable millisecond timestamp helper.
# date +%s%3N is GNU-only; macOS date does not support %N.
# perl Time::HiRes is available on all supported platforms.
# ---------------------------------------------------------------------------

ms_now() { perl -MTime::HiRes=time -e 'printf "%d\n", time() * 1000'; }

# ---------------------------------------------------------------------------
# Prestop sleep
# ---------------------------------------------------------------------------

@test "prestop_sleep delays shutdown by at least configured duration" {
  local cfg pid
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\ntimeouts:\n  prestop_sleep: 300ms\n' > "${cfg}"
  KEEL_CONFIG="${cfg}" keel-min &
  pid="${!}"
  sleep 0.4
  local t_start t_end elapsed
  t_start="$(ms_now)"
  kill -TERM "${pid}"
  wait "${pid}" || true
  t_end="$(ms_now)"
  rm -f "${cfg}"
  elapsed="$(( t_end - t_start ))"
  [ "${elapsed}" -ge 250 ]
}

# ---------------------------------------------------------------------------
# Logging level via SIGHUP reload
# ---------------------------------------------------------------------------

@test "log level survives SIGHUP reload without crash" {
  # macOS: poll until port 9999 is free (previous test may hold it briefly).
  if [[ "$(uname)" == "Darwin" ]]; then
    local _p=0
    while lsof -nP -iTCP:9999 -sTCP:LISTEN > /dev/null 2>&1; do
      sleep 0.1; _p=$(( _p + 1 ))
      [ "${_p}" -lt 50 ] || break
    done
  fi
  local cfg pid
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\n  admin:\n    enabled: true\nlogging:\n  level: warn\n' > "${cfg}"
  KEEL_CONFIG="${cfg}" keel-min &
  pid="${!}"
  sleep 0.4
  kill -HUP "${pid}"
  sleep 0.2
  kill -0 "${pid}"
  kill -TERM "${pid}"
  wait "${pid}"
  local exit_code="${?}"
  rm -f "${cfg}"
  [ "${exit_code}" -eq 0 ]
}

# ---------------------------------------------------------------------------
# Remote log sink — HTTP delivery (keel-max)
# ---------------------------------------------------------------------------

@test "remote_sink protocol http: log lines delivered to HTTP endpoint (keel-max)" {
  command -v nc > /dev/null 2>&1 || skip "nc not available"
  command -v keel-max > /dev/null 2>&1 || skip "keel-max binary not in dist/"
  # macOS: brief delay to allow previous test's port 9999 socket to be released.
  [[ "$(uname)" == "Darwin" ]] && sleep 1.0

  local sink_port received_file cfg pid sink_pid
  sink_port=19877
  received_file="$(mktemp)"

  # One-shot HTTP acceptor: records the request (headers + body), then answers
  # 200 — only after the request arrives, or Go's client discards the early reply.
  { for _ in $(seq 1 100); do [ -s "${received_file}" ] && break; sleep 0.05; done
    printf 'HTTP/1.1 200 OK\r\nContent-Length: 0\r\nConnection: close\r\n\r\n'
  } | nc -l 127.0.0.1 "${sink_port}" > "${received_file}" &
  sink_pid="${!}"
  sleep 0.2

  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\nlogging:\n  remote_sink:\n    enabled: true\n    endpoint: "http://127.0.0.1:%s/ingest"\n    protocol: http\n' \
    "${sink_port}" > "${cfg}"

  KEEL_CONFIG="${cfg}" keel-max &
  pid="${!}"
  sleep 0.5

  kill -TERM "${pid}"
  wait "${pid}" || true
  kill "${sink_pid}" 2>/dev/null || true
  wait "${sink_pid}" 2>/dev/null || true
  rm -f "${cfg}"

  local received_bytes
  received_bytes="$(wc -c < "${received_file}" | tr -d '[:space:]')"
  rm -f "${received_file}"
  [ "${received_bytes}" -gt 0 ]
}

# ---------------------------------------------------------------------------
# Sidecar outbound signing (keel-max)
# ---------------------------------------------------------------------------

@test "sidecar outbound signing: Authorization Bearer header forwarded to upstream (keel-max)" {
  command -v openssl > /dev/null 2>&1 || skip "openssl not available"
  command -v nc > /dev/null 2>&1 || skip "nc not available"
  command -v keel-max > /dev/null 2>&1 || skip "keel-max binary not in dist/"

  local upstream_port keel_port keyfile header_file cfg pid upstream_pid
  upstream_port=19878
  keel_port=19879
  keyfile="$(mktemp)"
  header_file="$(mktemp)"

  # Generate ECDSA P-256 private key for outbound JWT signing.
  openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "${keyfile}" 2>/dev/null

  # Upstream: answers 200 to every connection (health probes and the proxied
  # request) and appends each request, incl. Authorization, to header_file.
  while :; do
    printf 'HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok' \
      | nc -l 127.0.0.1 "${upstream_port}" >> "${header_file}"
  done > /dev/null 2>&1 3>&- &
  upstream_pid="${!}"
  sleep 0.2

  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: true\n    port: %s\n  health:\n    enabled: false\n  ready:\n    enabled: false\nauthn:\n  enabled: false\n  my_id: test-svc\n  my_signature_key_file: %s\nsidecar:\n  enabled: true\n  upstream_url: "http://127.0.0.1:%s"\n' \
    "${keel_port}" "${keyfile}" "${upstream_port}" > "${cfg}"

  KEEL_CONFIG="${cfg}" keel-max &
  pid="${!}"
  sleep 0.5

  curl -s --max-time 2 "http://127.0.0.1:${keel_port}/" > /dev/null || true
  # nc answers before it reads the request, so the capture can land after the
  # response; wait (up to 2 s) for it rather than a fixed sleep.
  local _w=0
  until grep -q 'Bearer ' "${header_file}" || [ "${_w}" -ge 20 ]; do
    sleep 0.1; _w=$(( _w + 1 ))
  done

  kill -TERM "${pid}"
  wait "${pid}" || true
  kill "${upstream_pid}" 2>/dev/null || true
  pkill -f "nc -l 127.0.0.1 ${upstream_port}" 2>/dev/null || true
  wait "${upstream_pid}" 2>/dev/null || true
  rm -f "${cfg}" "${keyfile}"

  grep -q 'Bearer ' "${header_file}"
  rm -f "${header_file}"
}

@test "sidecar: a hung upstream returns 502 after timeouts.write instead of hanging (keel-max)" {
  command -v keel-max > /dev/null 2>&1 || skip "keel-max binary not in dist/"
  command -v nc > /dev/null 2>&1 || skip "nc not available"
  local upstream_port keel_port cfg pid upstream_pid status started elapsed
  upstream_port=19884
  keel_port=19885
  # Upstream accepts every connection and never answers.
  while :; do
    nc -l 127.0.0.1 "${upstream_port}" < /dev/null > /dev/null 2>&1 || true
  done > /dev/null 2>&1 3>&- &
  upstream_pid="${!}"
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: true\n    port: %s\n  health:\n    enabled: false\n  ready:\n    enabled: false\nauthn:\n  enabled: false\ntimeouts:\n  write: 1s\nsidecar:\n  enabled: true\n  upstream_url: "http://127.0.0.1:%s"\n' \
    "${keel_port}" "${upstream_port}" > "${cfg}"
  KEEL_CONFIG="${cfg}" keel-max > /dev/null 2>&1 &
  pid="${!}"
  sleep 0.5
  started="$(ms_now)"
  status="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "http://127.0.0.1:${keel_port}/")" || true
  elapsed="$(( $(ms_now) - started ))"
  kill -TERM "${pid}"
  wait "${pid}" || true
  kill "${upstream_pid}" 2>/dev/null || true
  pkill -f "nc -l 127.0.0.1 ${upstream_port}" 2>/dev/null || true
  wait "${upstream_pid}" 2>/dev/null || true
  rm -f "${cfg}"
  [ "${status}" = '502' ]
  [ "${elapsed}" -lt 5000 ]
}

# ---------------------------------------------------------------------------
# authn.trusted_signers_file — JWT verified against file-loaded HMAC key (keel-max)
# ---------------------------------------------------------------------------

@test "authn.trusted_signers_file: request signed with file key returns 200 (keel-max)" {
  command -v openssl > /dev/null 2>&1 || skip "openssl not available"
  command -v keel-max > /dev/null 2>&1 || skip "keel-max binary not in dist/"

  local keel_port signers_file cfg pid token hmac_key hdr pay sig now
  keel_port=19880
  signers_file="$(mktemp)"

  # Generate a random 32-byte hex HMAC key and write it to the signers file.
  hmac_key="$(openssl rand -hex 16)"
  printf '%s\n' "${hmac_key}" > "${signers_file}"

  # Generate a valid HS256 JWT signed with hmac_key.
  b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }
  now="$(date +%s)"
  hdr="$(printf '{"alg":"HS256","typ":"JWT"}' | b64url)"
  pay="$(printf '{"sub":"test","iss":"test","iat":%s,"exp":%s}' "${now}" "$(( now + 300 ))" | b64url)"
  sig="$(printf '%s.%s' "${hdr}" "${pay}" | openssl dgst -sha256 -hmac "${hmac_key}" -binary | b64url)"
  token="${hdr}.${pay}.${sig}"

  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: true\n    port: %s\n  health:\n    enabled: false\n  ready:\n    enabled: false\nauthn:\n  enabled: true\n  trusted_signers_file: %s\n' \
    "${keel_port}" "${signers_file}" > "${cfg}"

  KEEL_CONFIG="${cfg}" keel-max &
  pid="${!}"
  sleep 0.4

  local status
  status="$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 \
    -H "Authorization: Bearer ${token}" \
    "http://127.0.0.1:${keel_port}/")"

  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}" "${signers_file}"
  [ "${status}" -eq 200 ]
}

# ---------------------------------------------------------------------------
# remote_sink protocol syslog: data arrives at TCP endpoint (keel-max)
# ---------------------------------------------------------------------------

@test "remote_sink protocol syslog: data delivered to TCP endpoint (keel-max)" {
  command -v nc > /dev/null 2>&1 || skip "nc not available"
  command -v keel-max > /dev/null 2>&1 || skip "keel-max binary not in dist/"
  # macOS: brief delay to allow previous test's port 9999 socket to be released.
  [[ "$(uname)" == "Darwin" ]] && sleep 1.0

  local syslog_port received_file cfg pid syslog_pid
  syslog_port=19881
  received_file="$(mktemp)"

  # One-shot TCP acceptor: records what keel sends until it closes the connection.
  nc -l 127.0.0.1 "${syslog_port}" < /dev/null > "${received_file}" &
  syslog_pid="${!}"
  sleep 0.2

  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\nlogging:\n  remote_sink:\n    enabled: true\n    endpoint: "127.0.0.1:%s"\n    protocol: syslog\n' \
    "${syslog_port}" > "${cfg}"

  KEEL_CONFIG="${cfg}" keel-max &
  pid="${!}"
  sleep 0.5

  kill -TERM "${pid}"
  wait "${pid}" || true
  wait "${syslog_pid}" 2>/dev/null || true
  rm -f "${cfg}"

  local received_bytes
  received_bytes="$(wc -c < "${received_file}" | tr -d '[:space:]')"
  rm -f "${received_file}"
  [ "${received_bytes}" -gt 0 ]
}

# ---------------------------------------------------------------------------
# prestop_sleep: 0s default — shutdown completes promptly (keel-min)
# ---------------------------------------------------------------------------

@test "prestop_sleep: 0s default shutdown completes in under 1 second" {
  local cfg pid
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\n' > "${cfg}"
  KEEL_CONFIG="${cfg}" keel-min &
  pid="${!}"
  sleep 0.4
  local t_start t_end elapsed
  t_start="$(ms_now)"
  kill -TERM "${pid}"
  wait "${pid}" || true
  t_end="$(ms_now)"
  rm -f "${cfg}"
  elapsed="$(( t_end - t_start ))"
  [ "${elapsed}" -lt 1000 ]
}

# ---------------------------------------------------------------------------
# fips.monitor: startup gate — non-FIPS binary must exit non-zero
# ---------------------------------------------------------------------------

@test "fips.monitor: keel-min with monitor=true exits non-zero (FIPS not compiled in)" {
  local cfg
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\nfips:\n  monitor: true\n' > "${cfg}"
  run env -u GOFIPS140 -u GODEBUG KEEL_CONFIG="${cfg}" keel-min
  rm -f "${cfg}"
  [ "${status}" -ne 0 ]
}

# ---------------------------------------------------------------------------
# ACME http-01 challenge handler (RFC 8555 §8.3)
# https://www.rfc-editor.org/rfc/rfc8555#section-8.3
# ---------------------------------------------------------------------------

# RFC 8555 §8.3: unknown challenge token must return 404.
# challenge_port 5080 stands in for :80 (non-root; allowed under BATS).
@test "ACME http-01: GET /.well-known/acme-challenge/<unknown> returns 404" {
  command -v keel-max > /dev/null 2>&1 || skip "keel-max binary not in dist/"
  local cfg pid
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  https:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\ntls:\n  acme:\n    enabled: true\n    challenge_port: 5080\n    domains: [test.example.com]\n    email: test@example.com\n    ca_url: "http://127.0.0.1:19991/dead"\n' > "${cfg}"
  KEEL_CONFIG="${cfg}" keel-max &
  pid="${!}"
  sleep 0.4
  local status
  status="$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 --retry 10 --retry-connrefused --retry-delay 1 http://127.0.0.1:5080/.well-known/acme-challenge/nosuchthing)"
  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}"
  [ "${status}" -eq 404 ]
}

# Non-challenge paths on the challenge port must redirect to HTTPS (301).
@test "ACME http-01: non-challenge GET / on port 80 redirects to HTTPS (301)" {
  command -v keel-max > /dev/null 2>&1 || skip "keel-max binary not in dist/"
  local cfg pid
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  https:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\ntls:\n  acme:\n    enabled: true\n    challenge_port: 5080\n    domains: [test.example.com]\n    email: test@example.com\n    ca_url: "http://127.0.0.1:19991/dead"\n' > "${cfg}"
  KEEL_CONFIG="${cfg}" keel-max &
  pid="${!}"
  sleep 0.4
  local status
  status="$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 --retry 10 --retry-connrefused --retry-delay 1 --no-location http://127.0.0.1:5080/)"
  kill -TERM "${pid}"
  wait "${pid}" || true
  rm -f "${cfg}"
  [ "${status}" -eq 301 ]
}

# RFC 8555 §8.3 — end-to-end ACME http-01 cert issuance using pebble test CA.
# PEBBLE_VA_SKIPVALIDATION=1: pebble marks challenges valid without contacting
# the challenge port. keel-max uses challenge_port: 5080 (non-privileged) so
# no root/sudo is required on any platform.
# Skipped when pebble binary or module cache is absent (dev/CI install pebble
# with: go install github.com/letsencrypt/pebble/cmd/pebble@v1.0.1).
@test "ACME end-to-end: pebble issues cert; keel writes cache_dir/cert.crt" {
  command -v keel-max > /dev/null 2>&1 || skip "keel-max binary not in dist/"
  command -v pebble > /dev/null 2>&1 || skip "pebble not installed (go install github.com/letsencrypt/pebble/cmd/pebble@v1.0.1)"

  local gomodcache pebble_dir pebble_cfg ca_cert cache_dir cfg pid pebble_pid i

  gomodcache="$(go env GOMODCACHE 2>/dev/null)" || skip "go not available"
  pebble_dir="$(ls -d "${gomodcache}/github.com/letsencrypt/pebble@"* 2>/dev/null | sort -V | tail -1)" || true
  [ -n "${pebble_dir}" ] || skip "pebble module not in module cache"

  ca_cert="${pebble_dir}/test/certs/pebble.minica.pem"
  [ -f "${ca_cert}" ] || skip "pebble minica CA cert not found at ${ca_cert}"

  # Write pebble config: httpPort matches keel's challenge_port (5080).
  pebble_cfg="$(mktemp)"
  printf '{"pebble":{"listenAddress":"127.0.0.1:14000","managementListenAddress":"127.0.0.1:15000","certificate":"%s/test/certs/localhost/cert.pem","privateKey":"%s/test/certs/localhost/key.pem","httpPort":5080,"tlsPort":5001,"externalAccountBindingRequired":false}}\n' \
    "${pebble_dir}" "${pebble_dir}" > "${pebble_cfg}"

  PEBBLE_VA_NOSLEEP=1 PEBBLE_VA_SKIPVALIDATION=1 pebble -config "${pebble_cfg}" > /tmp/pebble.log 2>&1 &
  pebble_pid="${!}"
  # Wait for pebble ACME directory to become reachable (up to 5 s).
  i=0
  while ! curl -sk --max-time 1 https://127.0.0.1:14000/dir > /dev/null 2>&1; do
    sleep 0.2
    i=$(( i + 1 ))
    if [ "${i}" -ge 25 ]; then
      kill "${pebble_pid}" 2>/dev/null || true
      rm -f "${pebble_cfg}"
      skip "pebble did not start within 5 s"
    fi
  done

  cache_dir="$(mktemp -d)"
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  https:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\ntls:\n  acme:\n    enabled: true\n    domains: [localhost]\n    email: test@example.com\n    ca_url: "https://127.0.0.1:14000/dir"\n    ca_cert_file: "%s"\n    cache_dir: "%s"\n    challenge_port: 5080\n' \
    "${ca_cert}" "${cache_dir}" > "${cfg}"

  KEEL_CONFIG="${cfg}" keel-max > /tmp/keel-acme.log 2>&1 &
  pid="${!}"

  # Poll for cert.crt up to 10 s.
  i=0
  while [ ! -s "${cache_dir}/cert.crt" ]; do
    sleep 0.5
    i=$(( i + 1 ))
    if [ "${i}" -ge 20 ]; then break; fi
  done

  kill -TERM "${pid}" 2>/dev/null || true
  wait "${pid}" 2>/dev/null || true
  kill -TERM "${pebble_pid}" 2>/dev/null || true
  wait "${pebble_pid}" 2>/dev/null || true
  rm -f "${cfg}" "${pebble_cfg}"

  [ -s "${cache_dir}/cert.crt" ]
  rm -rf "${cache_dir}"
}

# ACME cache reuse: keel must serve a cached cert without contacting the CA.
# A valid ECDSA P-256 cert is pre-written to cache_dir. keel is started with
# a dead ca_url so any CA contact would cause a fatal error. If keel stays
# alive and the cert file is intact, the cache-load path is working.
# challenge_port 5080 stands in for :80 so no root is needed.
@test "ACME cache reuse: pre-existing cert.crt served without contacting CA" {
  command -v keel-max > /dev/null 2>&1 || skip "keel-max binary not in dist/"
  command -v openssl > /dev/null 2>&1 || skip "openssl not available"

  local cache_dir cfg pid

  cache_dir="$(mktemp -d)"

  # Generate a fresh ECDSA P-256 self-signed cert covering "localhost" with a
  # 90-day validity so certNeedsRenewal returns false and no CA contact occurs.
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 \
    -keyout "${cache_dir}/cert.key" \
    -out "${cache_dir}/cert.crt" \
    -days 90 -nodes \
    -subj "/CN=localhost" \
    -addext "subjectAltName=DNS:localhost" \
    > /dev/null 2>&1

  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: false\n  https:\n    enabled: false\n  health:\n    enabled: false\n  ready:\n    enabled: false\ntls:\n  acme:\n    enabled: true\n    challenge_port: 5080\n    domains: [localhost]\n    email: test@example.com\n    ca_url: "http://127.0.0.1:19991/dead"\n    cache_dir: "%s"\n' \
    "${cache_dir}" > "${cfg}"

  KEEL_CONFIG="${cfg}" keel-max > /tmp/keel-acme-reuse.log 2>&1 &
  pid="${!}"
  sleep 0.5

  # Verify process is still alive (did not exit due to validation error).
  kill -0 "${pid}" 2>/dev/null
  local alive="${?}"

  kill -TERM "${pid}" 2>/dev/null || true
  wait "${pid}" 2>/dev/null || true
  rm -f "${cfg}"

  [ "${alive}" -eq 0 ]
  [ -s "${cache_dir}/cert.crt" ]
  rm -rf "${cache_dir}"
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
# ---------------------------------------------------------------------------
# Listener bind failure must stop the process (no running without a listener)
# ---------------------------------------------------------------------------

@test "listener bind failures (ports in use) are all reported, then the process exits non-zero" {
  local http_port health_port cfg holder
  http_port=19882
  health_port=19883
  cfg="$(mktemp)"
  printf 'listeners:\n  http:\n    enabled: true\n    port: %s\n  health:\n    enabled: true\n    port: %s\n  ready:\n    enabled: false\n' \
    "${http_port}" "${health_port}" > "${cfg}"

  # First instance holds both ports.
  KEEL_CONFIG="${cfg}" keel-min > /dev/null 2>&1 &
  holder="${!}"
  curl -s -o /dev/null --max-time 2 --retry 10 --retry-connrefused --retry-delay 1 "http://127.0.0.1:${health_port}/healthz" || true

  # Second instance cannot bind either port; every listener is attempted, both
  # failures are reported, and it exits on its own (124 = still running).
  run timeout 10 env KEEL_CONFIG="${cfg}" keel-min

  kill -TERM "${holder}" 2>/dev/null || true
  wait "${holder}" 2>/dev/null || true
  rm -f "${cfg}"

  [ "${status}" -ne 0 ]
  [ "${status}" -ne 124 ]
  [[ "${output}" == *":${http_port}"* ]]
  [[ "${output}" == *":${health_port}"* ]]
}
