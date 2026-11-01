#!/usr/bin/env bash
# scripts/release/verify-release.sh
# The release verifies its own output as its last step. Fails unless every
# published artifact carries this workflow's signatures and attestations:
#   - each image index (max, min, fips): cosign signature, OpenVEX attestation,
#     GitHub SLSA provenance; each arch image behind it: cosign signature and
#     SPDX SBOM attestation;
#   - vX.Y, vX and latest resolve to the max index;
#   - each release binary and SHA256SUMS: cosign bundle and GitHub provenance;
#   - the Helm chart: cosign signature.
#
# Required environment variables:
#   MAX_DIGEST, MIN_DIGEST, FIPS_DIGEST — index digests (sha256:...) from docker.sh
#   GH_TOKEN                           — for gh attestation verify
#
# Requires: cosign, docker (buildx imagetools), gh, jq.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly IMAGE='ghcr.io/keelcore/keel'
readonly CHART='ghcr.io/keelcore/charts/keel'
readonly REPO='keelcore/keel'
readonly DIST='dist'
readonly SBOM_PREDICATE='https://spdx.dev/Document'
readonly -a VERIFY_IDENTITY=(
  --certificate-identity-regexp '^https://github\.com/keelcore/keel/\.github/workflows/release\.yml@refs/tags/v'
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
)

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local tag
  tag="$(git describe --tags --exact-match)"
  log "Verifying release ${tag}"
  verify_index max "${MAX_DIGEST}" "${IMAGE}:${tag}"
  verify_index min "${MIN_DIGEST}" "${IMAGE}:${tag}-min"
  verify_index fips "${FIPS_DIGEST}" "${IMAGE}:${tag}-fips"
  verify_aliases "${tag}" "${MAX_DIGEST}"
  verify_blobs
  verify_chart "${tag#v}"
  log "✅ Release ${tag} verified"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_verify_release.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

function verify_index() {
  local -r flavor="${1}"
  local -r ref="${IMAGE}@${2}"
  local -r tag_ref="${3}"
  log "🔍 ${flavor}: ${ref}"
  expect_digest "${tag_ref}" "${2}"
  cosign verify "${VERIFY_IDENTITY[@]}" "${ref}" >/dev/null
  cosign verify-attestation "${VERIFY_IDENTITY[@]}" --type openvex "${ref}" >/dev/null
  gh attestation verify "oci://${ref}" --repo "${REPO}" >/dev/null
  local arch_digest
  for arch_digest in $(arch_digests "${tag_ref}"); do
    verify_arch "${IMAGE}@${arch_digest}"
  done
}

function verify_arch() {
  local -r ref="${1}"
  log "   arch image ${ref}"
  cosign verify "${VERIFY_IDENTITY[@]}" "${ref}" >/dev/null
  cosign verify-attestation "${VERIFY_IDENTITY[@]}" --type "${SBOM_PREDICATE}" "${ref}" >/dev/null
}

# arch_digests prints the platform manifest digests behind an index.
function arch_digests() {
  docker buildx imagetools inspect "${1}" --raw \
    | jq -r '.manifests[] | select(.platform.os == "linux") | .digest'
}

# expect_digest fails unless ref resolves to the expected index digest.
function expect_digest() {
  local -r ref="${1}"
  local -r want="${2}"
  local got
  got="$(docker buildx imagetools inspect "${ref}" --format '{{json .Manifest}}' | jq -r '.digest')"
  [ "${got}" = "${want}" ] || { log "❌ ${ref} resolves to ${got}, expected ${want}"; exit 1; }
}

function verify_aliases() {
  local -r tag="${1}"
  local -r want="${2}"
  local alias
  for alias in "$(grep -oE '^v[0-9]+\.[0-9]+' <<< "${tag}")" "$(grep -oE '^v[0-9]+' <<< "${tag}")" 'latest'; do
    log "🔍 alias ${IMAGE}:${alias}"
    expect_digest "${IMAGE}:${alias}" "${want}"
  done
}

function verify_blobs() {
  local blob
  for blob in "${DIST}"/keel-*-linux-* "${DIST}/SHA256SUMS"; do
    [[ "${blob}" == *.bundle || "${blob}" == *.sigstore.json ]] && continue
    log "🔍 blob ${blob}"
    cosign verify-blob "${VERIFY_IDENTITY[@]}" --bundle "${blob}.bundle" "${blob}" >/dev/null
    [ "${blob}" = "${DIST}/SHA256SUMS" ] || gh attestation verify "${blob}" --repo "${REPO}" >/dev/null
  done
}

function verify_chart() {
  local -r version="${1}"
  log "🔍 chart ${CHART}:${version}"
  cosign verify "${VERIFY_IDENTITY[@]}" "${CHART}:${version}" >/dev/null
}

main "${@:-}"
