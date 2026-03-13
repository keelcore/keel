#!/usr/bin/env bash
# sign.sh
# Sign release artifacts in dist/ using cosign keyless signing (Sigstore OIDC).
# Consumers verify with: cosign verify-blob --bundle <file>.bundle <file>
#
# Requires: cosign in PATH; OIDC identity token available (GitHub Actions provides
# this automatically when the job has id-token: write permission).

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
  log "Signing release artifacts (cosign keyless)"
  require_cosign
  sign_artifacts
  log "All artifacts signed"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_sign.log' >&5
}

function validate_args() { :; }

function require_cosign() {
  if ! command -v cosign >/dev/null 2>&1; then
    log "ERROR: cosign not found in PATH"
    log "  Install via: scripts/release/install-cosign.sh"
    exit 1
  fi
}

function sign_artifacts() {
  local artifact
  local found=0

  for artifact in dist/keel-*; do
    # Skip bundles, signatures, and the SBOM (signed separately if needed).
    [[ "${artifact}" == *.bundle ]]    && continue
    [[ "${artifact}" == *.sig ]]       && continue
    [[ "${artifact}" == *.spdx.json ]] && continue
    [ -f "${artifact}" ]               || continue

    log "  Signing ${artifact}"
    # --yes suppresses the interactive prompt in non-TTY environments.
    cosign sign-blob \
      --yes \
      --bundle "${artifact}.bundle" \
      "${artifact}"

    found=1
  done

  if [ "${found}" -eq 0 ]; then
    log "ERROR: no artifacts found in dist/ to sign"
    exit 1
  fi
}

main "${@:-}"
