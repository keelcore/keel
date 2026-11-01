#!/usr/bin/env bash
# docker.sh
# Build, tag, push, and sign container images for all three keel flavors.
# Uses pre-built binaries from dist/ — build-once, package-once.
#
# Per flavor, in order: build → SBOM from the local image + grype scan (fail on
# fixable HIGH/CRITICAL) → push primary tag → sign + attest that SBOM by digest
# → record the digest in $GITHUB_OUTPUT → move aliases (max only). Aliases move
# last, so a failure never leaves vX.Y / vX / latest on an unsigned image.
#
# Required environment variables:
#   GITHUB_TOKEN   — GHCR write token (GITHUB_TOKEN in Actions)
#   GITHUB_ACTOR   — registry username (github.actor in Actions)
#
# Image targets:
#   ghcr.io/keelcore/keel:v1.2.3        (max / default)
#   ghcr.io/keelcore/keel:v1.2          (max)
#   ghcr.io/keelcore/keel:v1            (max)
#   ghcr.io/keelcore/keel:latest        (max)
#   ghcr.io/keelcore/keel:v1.2.3-min
#   ghcr.io/keelcore/keel:v1.2.3-fips

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly REGISTRY='ghcr.io'
readonly IMAGE="${REGISTRY}/keelcore/keel"
readonly DOCKERFILE='scripts/deploy/docker/Dockerfile.release'
readonly DIST='dist'
readonly SBOM_PREDICATE='https://spdx.dev/Document'
# Identity of a signature made by this repo's release workflow (keyless, GitHub OIDC).
readonly -a VERIFY_IDENTITY=(
  --certificate-identity-regexp '^https://github\.com/keelcore/keel/\.github/workflows/release\.yml@refs/tags/v'
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
)

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local tag
  tag="$(resolve_tag)"
  log "Publishing container images for ${tag}"
  require_env
  require_docker
  require_cosign
  require_tool syft 'make setup-syft'
  require_tool grype 'make setup-grype'
  registry_login
  bash scripts/build/certs-image.sh
  build_and_push_flavor 'max'  "${DIST}/keel-max-linux-amd64"  "${tag}"
  build_and_push_flavor 'min'  "${DIST}/keel-min-linux-amd64"  "${tag}"
  build_and_push_flavor 'fips' "${DIST}/keel-fips-linux-amd64" "${tag}"
  log "All container images published and signed"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_docker.log' >&5
}

function validate_args() { :; }

function require_env() {
  local missing=0
  for var in GITHUB_TOKEN GITHUB_ACTOR; do
    if [ -z "${!var:-}" ]; then
      log "ERROR: ${var} is required"
      missing=1
    fi
  done
  [ "${missing}" -eq 0 ] || exit 1
}

function require_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    log "ERROR: docker not found in PATH"
    exit 1
  fi
}

function require_cosign() {
  if ! command -v cosign >/dev/null 2>&1; then
    log "ERROR: cosign not found in PATH — run scripts/release/install-cosign.sh"
    exit 1
  fi
}

function require_tool() {
  local -r tool="${1}"
  local -r install="${2}"
  if ! command -v "${tool}" >/dev/null 2>&1; then
    log "ERROR: ${tool} not found in PATH — run ${install}"
    exit 1
  fi
}

function registry_login() {
  log "Logging in to ${REGISTRY}"
  printf '%s' "${GITHUB_TOKEN}" | \
    docker login "${REGISTRY}" --username "${GITHUB_ACTOR}" --password-stdin
}

# resolve_tag echoes a v* tag or synthesizes one from git describe.
function resolve_tag() {
  git describe --tags --dirty --always
}

# semver_minor echoes the vMAJOR.MINOR portion of a version tag.
function semver_minor() {
  printf '%s' "${1}" | grep -oE '^v[0-9]+\.[0-9]+'
}

# semver_major echoes the vMAJOR portion of a version tag.
function semver_major() {
  printf '%s' "${1}" | grep -oE '^v[0-9]+'
}

function build_and_push_flavor() {
  local -r flavor="${1}"
  local -r binary="${2}"
  local -r tag="${3}"
  local -r primary_tag="$(primary_tag_for "${flavor}" "${tag}")"
  build_image "${flavor}" "${binary}" "${tag}" "${primary_tag}"
  scan_image "${flavor}" "${primary_tag}"
  local digest
  digest="$(push_image "${primary_tag}")"
  sign_and_attest "${flavor}" "${digest}"
  record_digest "${flavor}" "${digest}"
  push_aliases "${flavor}" "${tag}" "${primary_tag}"
}

function primary_tag_for() {
  local -r flavor="${1}"
  local -r tag="${2}"
  if [ "${flavor}" = 'max' ]; then
    printf '%s' "${IMAGE}:${tag}"
  else
    printf '%s' "${IMAGE}:${tag}-${flavor}"
  fi
}

function build_image() {
  local -r flavor="${1}"
  local -r binary="${2}"
  local -r tag="${3}"
  local -r primary_tag="${4}"
  [ -f "${binary}" ] || { log "ERROR: binary not found: ${binary}"; exit 1; }
  local ctx
  ctx="$(mktemp -d)"
  cp "${binary}" "${ctx}/keel"
  log "  Building ${primary_tag} (flavor=${flavor})"
  docker build \
    --file "${DOCKERFILE}" \
    --build-arg "FLAVOR=${flavor}" \
    --tag "${primary_tag}" \
    --label "org.opencontainers.image.version=${tag}" \
    --label "org.opencontainers.image.created=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --label "org.opencontainers.image.revision=$(git rev-parse HEAD)" \
    "${ctx}" >&5
  rm -rf "${ctx}"
}

# scan_image writes the image's own SBOM (dist/sbom-<flavor>.spdx.json) from the
# local image and fails the release on any fixable HIGH or CRITICAL finding —
# before anything is pushed.
function scan_image() {
  local -r flavor="${1}"
  local -r ref="${2}"
  local -r sbom="$(sbom_path "${flavor}")"
  log "  SBOM ${ref} → ${sbom}"
  syft scan "docker:${ref}" --output "spdx-json=${sbom}" >&5
  log "  Scanning ${ref} (grype, fail on fixable high+)"
  grype "sbom:${sbom}" --only-fixed --fail-on high >&5
}

function sbom_path() {
  printf '%s/sbom-%s.spdx.json' "${DIST}" "${1}"
}

# push_image pushes the primary tag and echoes its immutable digest reference.
function push_image() {
  local -r ref="${1}"
  log "  Pushing ${ref}"
  docker push "${ref}" >&5
  docker inspect --format='{{index .RepoDigests 0}}' "${ref}"
}

# push_aliases moves vX.Y, vX and latest to the already-signed max image.
# Runs last, so a failed sign or scan never leaves an alias on an unsigned image.
function push_aliases() {
  local -r flavor="${1}"
  local -r tag="${2}"
  local -r primary_tag="${3}"
  [ "${flavor}" = 'max' ] && [[ "${tag}" =~ ^v[0-9] ]] || return 0
  local alias
  for alias in "$(semver_minor "${tag}")" "$(semver_major "${tag}")" 'latest'; do
    docker tag "${primary_tag}" "${IMAGE}:${alias}"
    log "  Pushing ${IMAGE}:${alias}"
    docker push "${IMAGE}:${alias}" >&5
  done
}

# record_digest exposes <flavor>_digest=sha256:... to later workflow steps
# (image provenance attestation).
function record_digest() {
  local -r flavor="${1}"
  local -r digest="${2}"
  [ -n "${GITHUB_OUTPUT:-}" ] || return 0
  printf '%s_digest=%s\n' "${flavor}" "${digest##*@}" >> "${GITHUB_OUTPUT}"
}

function sign_and_attest() {
  local -r flavor="${1}"
  local -r digest="${2}"
  sign_image "${digest}"
  attest_sbom "${digest}" "$(sbom_path "${flavor}")"
}

# sign_image is idempotent: a retry that hits Rekor 409 (entry already exists
# for this digest) succeeds when cosign verify confirms the existing signature.
function sign_image() {
  local -r digest="${1}"
  log "  Signing ${digest} (cosign keyless)"
  if cosign sign --yes "${digest}"; then
    return 0
  fi
  log "  cosign sign failed; checking for an existing signature on ${digest}"
  cosign verify "${VERIFY_IDENTITY[@]}" "${digest}" >/dev/null
  log "  ✅ ${digest} already signed by this workflow"
}

function attest_sbom() {
  local -r digest="${1}"
  local -r sbom="${2}"
  log "  Attesting ${sbom} to ${digest}"
  # Use the full predicate type URI rather than the 'spdx' shortname.
  # cosign v3.x shortname validation rejects syft SPDX 2.3 JSON;
  # the URI form treats the predicate as an opaque JSON payload.
  if cosign attest --yes --predicate "${sbom}" --type "${SBOM_PREDICATE}" "${digest}"; then
    return 0
  fi
  log "  cosign attest failed; checking for an existing SBOM attestation on ${digest}"
  cosign verify-attestation "${VERIFY_IDENTITY[@]}" --type "${SBOM_PREDICATE}" "${digest}" >/dev/null
  log "  ✅ ${digest} already carries this workflow's SBOM attestation"
}

main "${@:-}"
