#!/usr/bin/env bash
# check-secrets.sh
# Scan repository for committed secrets using gitleaks.
# Runnable locally and in CI identically.
#
# Runs in the dev image, which carries the pinned gitleaks (scripts/ci/setup-gitleaks.sh).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

function main() {
  exec 5>&1
  validate_args "${@:-}"
  ensure_gitleaks
  log "Secret scan (gitleaks $(gitleaks version))"
  run_scan
  log "Secret scan passed — no secrets detected"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_secret_scan.log' >&5
}

function validate_args() { :; }

function ensure_gitleaks() {
  command -v gitleaks >/dev/null 2>&1 && return 0
  log 'ERROR: gitleaks not found; the secret scan runs in the dev image: make check-secrets'
  exit 1
}

function run_scan() {
  # --exit-code 1 causes gitleaks to return 1 when leaks are found.
  # --no-banner suppresses decorative output; CI logs stay clean.
  # --config .gitleaks.toml applies project allowlists (false-positive suppression).
  gitleaks detect \
    --source . \
    --no-banner \
    --exit-code 1 \
    --config '.gitleaks.toml'
}

main "${@:-}"
