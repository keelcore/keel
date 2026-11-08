#!/usr/bin/env bash
# scripts/release/verify-release.sh
# The release verifies its own output as its last step. Fails unless every
# published artifact carries this workflow's signatures and attestations:
#   - each image index: cosign signature, OpenVEX attestation,
#     GitHub SLSA provenance; each arch image behind it: cosign signature and
#     SPDX SBOM attestation;
#   - vX.Y, vX and latest resolve to the max index;
#   - each release binary and SHA256SUMS: cosign bundle and GitHub provenance;
#   - the Helm chart: cosign signature.
#
# Required environment variables:
#   <VARIANT>_DIGEST                   — each variant's index digest (sha256:...) from publish-release-images.sh
#   GH_TOKEN                           — for gh attestation verify
#   GITHUB_TOKEN, GITHUB_ACTOR         — GHCR login for cosign (scripts/release/run-cosign.sh)
#
# Runs on the host (docker buildx imagetools, gh, jq); cosign runs in the dev image
# (scripts/dev/run.sh) on the published refs and the files in dist/.
#
# Checks run concurrently (scripts/lib/parallel.sh).
# Project hook: verify-release.local.sh runs first, when present, with this script's arguments
# (scripts/lib/local-hook.sh: the same path, in the repository this runs in).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/local-hook.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/local-hook.sh"

# 4) Keep errexit on inside command substitutions, so a failed lookup cannot pass as an empty list.
shopt -s inherit_errexit

# shellcheck source=../lib/parallel.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/parallel.sh"
# shellcheck source=../lib/tool-config.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/tool-config.sh"

REPO="$(release_repo)"
readonly REPO
readonly IMAGE="ghcr.io/${REPO}"
PROJECT="$(project_name)"
readonly PROJECT
CHART="ghcr.io/${REPO%%/*}/charts/$(chart_name)"
readonly CHART
readonly DIST='dist'
readonly SBOM_PREDICATE='https://spdx.dev/Document'
readonly -a VERIFY_IDENTITY=(
  --certificate-identity-regexp "$(release_identity)"
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
)
# One at a time: concurrent runs of gh attestation verify failed to initialize their verifier on the runner.
GH_ATTEST_LOCK="$(mktemp)"
readonly GH_ATTEST_LOCK

# cosign: the dev image's, logged in to GHCR (scripts/release/run-cosign.sh; the packages may be private).
function cosign() {
  bash scripts/dev/run.sh bash scripts/release/run-cosign.sh "$@"
}

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  local tag
  tag="$(git describe --tags --exact-match)"
  log "Verifying release ${tag}"
  local -a steps=(aliases blobs chart)
  local variant
  while IFS= read -r variant; do
    steps+=("index-${variant}")
  done < <(release_variants | cut -f1)
  run_parallel verify_step "${steps[@]}" -- "${tag}"
  log "✅ Release ${tag} verified"
}

# verify_step runs one group of checks, as a run_parallel job.
function verify_step() {
  local -r tag="${2}"
  case "${1}" in
    index-*) verify_variant "${1#index-}" "${tag}" ;;
    aliases) verify_aliases "${tag}" "$(variant_digest "$(primary_variant)")" ;;
    blobs) verify_blobs ;;
    chart) [ -z "$(chart_name)" ] || verify_chart "${tag#v}" ;;
  esac
}

# variant_digest VARIANT: its index digest, from <VARIANT>_DIGEST (publish-release-images.sh records them).
function variant_digest() {
  local -r var="$(printf '%s' "${1}" | tr '[:lower:]' '[:upper:]')_DIGEST"
  printf '%s' "${!var:?${var} is required}"
}

function primary_variant() {
  release_variants | awk -F'\t' '$2 == "" { print $1; exit }'
}

function verify_variant() {
  local -r variant="${1}" tag="${2}"
  local -r suffix="$(release_variants | awk -F'\t' -v v="${variant}" '$1 == v { print $2; exit }')"
  verify_index "${variant}" "$(variant_digest "${variant}")" "${IMAGE}:${tag}${suffix}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/verify_release.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

# gh_attest SUBJECT: its GitHub provenance attestation, under the lock.
function gh_attest() {
  flock "${GH_ATTEST_LOCK}" gh attestation verify "${1}" --repo "${REPO}" >/dev/null
}

function verify_index() {
  local -r flavor="${1}"
  local -r ref="${IMAGE}@${2}"
  local -r tag_ref="${3}"
  log "🔍 ${flavor}: ${ref}"
  expect_digest "${tag_ref}" "${2}"
  cosign verify "${VERIFY_IDENTITY[@]}" "${ref}" >/dev/null
  cosign verify-attestation "${VERIFY_IDENTITY[@]}" --type openvex "${ref}" >/dev/null
  gh_attest "oci://${ref}"
  local digests
  digests="$(arch_digests "${tag_ref}")"
  local -a refs=()
  local arch_digest
  for arch_digest in ${digests}; do
    refs+=("${IMAGE}@${arch_digest}")
  done
  run_parallel verify_arch "${refs[@]}"
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
  local -a blobs=()
  local blob
  for blob in "${DIST}/${PROJECT}"-*-linux-* "${DIST}/SHA256SUMS"; do
    [[ "${blob}" == *.bundle || "${blob}" == *.sigstore.json ]] || blobs+=("${blob}")
  done
  run_parallel verify_blob "${blobs[@]}"
}

function verify_blob() {
  local -r blob="${1}"
  log "🔍 blob ${blob}"
  cosign verify-blob "${VERIFY_IDENTITY[@]}" --bundle "${blob}.bundle" "${blob}" >/dev/null
  [ "${blob}" = "${DIST}/SHA256SUMS" ] || gh_attest "${blob}"
}

function verify_chart() {
  local -r version="${1}"
  log "🔍 chart ${CHART}:${version}"
  cosign verify "${VERIFY_IDENTITY[@]}" "${CHART}:${version}" >/dev/null
}

main "${@:-}"
