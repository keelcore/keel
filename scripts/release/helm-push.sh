#!/usr/bin/env bash
# helm-push.sh
# Package the Keel Helm chart and push it to GHCR as an OCI artifact.
# Signs the pushed chart digest with cosign keyless signing.
#
# Required environment variables:
#   GITHUB_TOKEN   — GHCR write token (GITHUB_TOKEN in Actions)
#   GITHUB_ACTOR   — registry username (github.actor in Actions)
#
# OCI target:
#   oci://ghcr.io/keelcore/charts/keel:<version>
#
# The chart version stored in Chart.yaml is overridden at package time
# with the semver tag so chart version == app version always.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly REGISTRY='ghcr.io'
readonly CHART_REPO="oci://${REGISTRY}/keelcore/charts"
readonly CHART_DIR='helm/keel'
readonly DIST='dist'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local tag
  tag="$(resolve_tag)"
  # Strip leading 'v' — Helm semver does not include the 'v' prefix.
  local version="${tag#v}"
  log "Publishing Helm chart version ${version} (tag=${tag})"
  require_env
  require_helm
  require_cosign
  registry_login
  package_chart "${version}"
  local pkg="dist/keel-${version}.tgz"
  push_chart "${pkg}" "${version}"
  sign_chart "${version}"
  log "Helm chart published and signed: ${CHART_REPO}/keel:${version}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_helm_push.log' >&5
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

function require_helm() {
  if ! command -v helm >/dev/null 2>&1; then
    log "ERROR: helm not found in PATH — install helm >= 3.8"
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
  log "Logging in to ${REGISTRY} (Helm OCI)"
  printf '%s' "${GITHUB_TOKEN}" | \
    helm registry login "${REGISTRY}" \
      --username "${GITHUB_ACTOR}" \
      --password-stdin
}

# resolve_tag echoes a v* tag or synthesizes one from git describe.
function resolve_tag() {
  git describe --tags --dirty --always
}

function package_chart() {
  local -r version="${1}"
  mkdir -p "${DIST}"
  log "  Packaging helm chart (version=${version})"
  helm package "${CHART_DIR}" \
    --version "${version}" \
    --app-version "${version}" \
    --destination "${DIST}"
}

function push_chart() {
  local -r pkg="${1}"
  local -r version="${2}"
  if [ ! -f "${pkg}" ]; then
    log "ERROR: packaged chart not found: ${pkg}"
    exit 1
  fi
  log "  Pushing ${pkg} to ${CHART_REPO}"
  helm push "${pkg}" "${CHART_REPO}"
}

function sign_chart() {
  local -r version="${1}"
  local -r ref="${REGISTRY}/keelcore/charts/keel:${version}"
  log "  Signing ${ref} (cosign keyless)"
  cosign sign --yes "${ref}"
}

main "${@:-}"