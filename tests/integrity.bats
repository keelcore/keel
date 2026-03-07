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
  run keel-min --validate --config "${bad_config}"
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
  keel-min --config "${config_file}" &
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
  keel-min --config "${config_file}" &
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
# FIPS build (skipped when keel-fips binary is absent)
# ---------------------------------------------------------------------------

@test "keel-fips fails-closed in non-FIPS environment" {
  command -v keel-fips > /dev/null 2>&1 || skip "keel-fips binary not in dist/"
  export FIPS_ENABLED=true
  run keel-fips
  [ "${status}" -eq 1 ]
  [[ "${output}" =~ "Failing closed" ]]
}