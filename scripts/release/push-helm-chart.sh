#!/usr/bin/env bash
# push-helm-chart.sh
# Package the repository's Helm chart and push it to GHCR as an OCI artifact.
# Signs the pushed chart digest with cosign keyless signing. Runs in the dev
# image (make push-helm-chart), which carries helm and cosign.
#
# Required environment variables:
#   GITHUB_TOKEN   — GHCR write token (GITHUB_TOKEN in Actions)
#   GITHUB_ACTOR   — registry username (github.actor in Actions)
#
# OCI target:
#   oci://ghcr.io/<owner>/charts/<chart name>:<version>
#
# The chart version stored in Chart.yaml is overridden at package time
# with the semver tag so chart version == app version always.
# Project hook: push-helm-chart.local.sh runs first, when present, with this script's arguments
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

# shellcheck source=../lib/tool-config.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/tool-config.sh"

readonly REGISTRY='ghcr.io'
OWNER="$(release_repo)"
OWNER="${OWNER%%/*}"
readonly OWNER
readonly CHART_REPO="oci://${REGISTRY}/${OWNER}/charts"
CHART_FILE="$(chart_file)"
readonly CHART_FILE
CHART_DIR="$(dirname "${CHART_FILE:-.}")"
readonly CHART_DIR
CHART_NAME="$(chart_name)"
readonly CHART_NAME
readonly DIST='dist'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  if [ -z "${CHART_NAME}" ]; then
    log 'ℹ️  No chart; nothing to publish'
    return 0
  fi
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
  local pkg="dist/${CHART_NAME}-${version}.tgz"
  push_chart "${pkg}" "${version}"
  sign_chart "${version}"
  log "Helm chart published and signed: ${CHART_REPO}/${CHART_NAME}:${version}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/helm_push.log' >&5
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
    log "ERROR: cosign not found in PATH — the dev image carries it (scripts/ci/setup-cosign.sh)"
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

# annotate_dev_image CHART_YAML: name the dev image that built this release in the chart's annotations.
function annotate_dev_image() {
  local -r file="${1}" entry="  io.keelcore.dev-image: $(release_dev_image)"
  if grep -q '^annotations:' "${file}"; then
    awk -v entry="${entry}" '{ print } /^annotations:/ { print entry }' "${file}" > "${file}.new"
    mv "${file}.new" "${file}"
  else
    printf 'annotations:\n%s\n' "${entry}" >> "${file}"
  fi
}

function package_chart() {
  local -r version="${1}"
  mkdir -p "${DIST}"
  log "  Packaging helm chart (version=${version})"
  local chart="${CHART_DIR}" work=''
  if [ -n "$(release_dev_image)" ]; then
    work="$(mktemp -d)"
    cp -R "${CHART_DIR}" "${work}/${CHART_NAME}"
    chart="${work}/${CHART_NAME}"
    annotate_dev_image "${chart}/Chart.yaml"
  fi
  helm package "${chart}" \
    --version "${version}" \
    --app-version "${version}" \
    --destination "${DIST}"
  [ -z "${work}" ] || rm -rf "${work}"
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
  local -r ref="${REGISTRY}/${OWNER}/charts/${CHART_NAME}:${version}"
  log "  Signing ${ref} (cosign keyless)"
  bash scripts/release/run-cosign.sh sign --yes "${ref}"
}

main "${@:-}"
