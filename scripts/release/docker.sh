#!/usr/bin/env bash
# docker.sh
# Build, tag, push, and sign container images for all three keel flavors.
# Uses pre-built binaries from dist/ — build-once, package-once.
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

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local tag
  tag="$(resolve_tag)"
  log "Publishing container images for ${tag}"
  require_env
  require_docker
  require_cosign
  registry_login
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

  if [ ! -f "${binary}" ]; then
    log "ERROR: binary not found: ${binary}"
    exit 1
  fi

  local ctx
  ctx="$(mktemp -d)"
  cp "${binary}" "${ctx}/keel"

  local primary_tag
  if [ "${flavor}" = 'max' ]; then
    primary_tag="${IMAGE}:${tag}"
  else
    primary_tag="${IMAGE}:${tag}-${flavor}"
  fi

  log "  Building ${primary_tag} (flavor=${flavor})"
  docker build \
    --file "${DOCKERFILE}" \
    --build-arg "FLAVOR=${flavor}" \
    --tag "${primary_tag}" \
    --label "org.opencontainers.image.version=${tag}" \
    --label "org.opencontainers.image.created=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --label "org.opencontainers.image.revision=$(git rev-parse HEAD)" \
    "${ctx}"

  rm -rf "${ctx}"

  log "  Pushing ${primary_tag}"
  docker push "${primary_tag}"

  # Push additional semver aliases for the default (max) flavor only,
  # and only when the tag is a proper semver version (starts with v+digit).
  if [ "${flavor}" = 'max' ] && [[ "${tag}" =~ ^v[0-9] ]]; then
    local minor major
    minor="$(semver_minor "${tag}")"
    major="$(semver_major "${tag}")"
    for alias in "${minor}" "${major}" 'latest'; do
      docker tag "${primary_tag}" "${IMAGE}:${alias}"
      log "  Pushing ${IMAGE}:${alias}"
      docker push "${IMAGE}:${alias}"
    done
  fi

  sign_image "${primary_tag}"
}

function sign_image() {
  local -r ref="${1}"
  log "  Signing ${ref} (cosign keyless)"
  # Resolve to digest to sign the immutable content address, not the mutable tag.
  local digest
  digest="$(docker inspect --format='{{index .RepoDigests 0}}' "${ref}")"
  cosign sign --yes "${digest}"
}

main "${@:-}"