#!/usr/bin/env bash
# scripts/dev/publish.sh
# Publish the repository's tool images (containers/*/Containerfile, and each build file
# config/tool-images.local.txt lists: tool_images in scripts/lib/dev-image.sh) to the registry, tagged
# by build context. Each is built from its build file, in its context directory.
#
# Usage:
#   publish.sh push ARCH   (CI, docker) build each image natively for ARCH (amd64 | arm64)
#                          and push <repository>:ctx-<hash>-ARCH; skipped when the
#                          multi-arch ctx-<hash> tag already exists
#   publish.sh merge       (CI, docker) join the per-arch images into the multi-arch
#                          <repository>:ctx-<hash> index and give it and each arch image behind it the
#                          canonical signature, SBOM and SLSA provenance (scripts/release/sign-image.sh,
#                          in the dev image)
#   publish.sh local       (macOS, Apple container) build every unpublished image for
#                          linux/amd64 and linux/arm64 and push it; the dev-image workflow signs it
#
# Env: GITHUB_TOKEN and GITHUB_ACTOR (registry login for push and merge; GitHub Actions sets
# GITHUB_ACTOR).
#
# Environment: DOCKER_CONFIG (default empty).
# Environment: GITHUB_ACTOR (default empty).
# Environment: GITHUB_STEP_SUMMARY (default empty).
# Environment: GITHUB_TOKEN (default empty).
# Project hook: after each mode, `bash scripts/dev/publish.local.sh MODE` runs when the repository
# holds it (resolved from the repository root, never canonical) — e.g. to check the published
# packages. In local mode the login uses GITHUB_TOKEN, else the developer's gh credential (read once,
# not exported).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/dev-image.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/dev-image.sh"

declare -rx GITHUB_TOKEN="${GITHUB_TOKEN:-}"
declare -rx GITHUB_STEP_SUMMARY="${GITHUB_STEP_SUMMARY:-}"
declare -rx GITHUB_ACTOR="${GITHUB_ACTOR:-}"
declare -rx DOCKER_CONFIG="${DOCKER_CONFIG:-}"
readonly LOCAL_HOOK='scripts/dev/publish.local.sh'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  case "${1}" in
    push) registry_login; for_each_image push_image "${2}" ;;
    merge) registry_login; for_each_image merge_image ;;
    local)
      # Credentials follow the container system's lifecycle: one login (one gh credential read)
      # when `container` holds none for the registry, never a logout; later runs reuse it.
      local_login
      for_each_image local_image ;;
  esac
  run_hook "${1}"
}

# run_hook MODE: the project's publish hook, when it has one.
function run_hook() {
  [ -f "${LOCAL_HOOK}" ] || return 0
  bash "${LOCAL_HOOK}" "${1}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keelcore_dev_publish.log' >&5
}

function validate_args() {
  case "${1:-}:${2:-}" in
    push:amd64 | push:arm64 | merge: | local:) ;;
    *)
      log '❌ Usage: publish.sh push amd64|arm64 | merge | local'
      exit 1
      ;;
  esac
}

function registry_login() {
  [ -z "${DOCKER_CONFIG}" ] || mkdir -p "${DOCKER_CONFIG}"
  printf '%s' "${GITHUB_TOKEN:?GITHUB_TOKEN is required}" \
    | docker login "$(dev_registry | cut -d/ -f1)" -u "${GITHUB_ACTOR:?GITHUB_ACTOR is required}" --password-stdin
}

# for_each_image FN [ARG...]: FN NAME HASH BASE ARG... for every tool image (tool_images); images that
# build on the .standards dev image come after the others. FN never reads the list: run.sh (docker run -i)
# would take the rest of it.
function for_each_image() {
  local -r fn="${1}"
  shift
  local list name base hash
  list="$(tool_images)"
  while IFS=$'\t' read -r name _; do
    [ -n "${name}" ] || continue
    dev_has_base . "${name}" && continue
    hash="$(dev_context_hash . "${name}")"
    "${fn}" "${name}" "${hash}" '' "$@" < /dev/null
  done <<< "${list}"
  while IFS=$'\t' read -r name _; do
    [ -n "${name}" ] || continue
    dev_has_base . "${name}" || continue
    base="$(standards_ref)"
    hash="$(dev_context_hash . "${name}" "${base}")"
    "${fn}" "${name}" "${hash}" "${base}" "$@" < /dev/null
  done <<< "${list}"
}

# standards_ref: the .standards dev image an image builds on. Never an unpublished base.
function standards_ref() {
  local hash
  hash="$(dev_context_hash .standards dev)"
  dev_published_ref "$(dev_repository .standards dev)" "${hash}" \
    || { log "❌ the .standards tool image ctx-${hash} is not published yet"; exit 1; }
}

function published() {
  docker manifest inspect "${1}" > /dev/null 2>&1
}

function push_image() {
  local -r name="${1}" hash="${2}" base="${3}" arch="${4}"
  local -r tag="$(dev_repository . "${name}"):ctx-${hash}"
  if published "${tag}"; then
    log "✅ ${tag} already published"
    return 0
  fi
  local -a args=(build --platform "linux/${arch}" -f "$(dev_containerfile "${name}")" -t "${tag}-${arch}")
  [ -z "${base}" ] || args+=(--build-arg "BASE_DEV_IMAGE=${base}")
  log "🔨 ${tag}-${arch}"
  docker "${args[@]}" "$(dev_context_dir "${name}")"
  docker push "${tag}-${arch}"
}

function merge_image() {
  local -r name="${1}" hash="${2}"
  local -r tag="$(dev_repository . "${name}"):ctx-${hash}"
  if published "${tag}"; then
    log "✅ ${tag} already published (signing and attesting it if it is not yet)"
  else
    docker buildx imagetools create -t "${tag}" "${tag}-amd64" "${tag}-arm64"
  fi
  local digest
  digest="$(docker buildx imagetools inspect "${tag}" --format '{{json .Manifest}}' | jq -r '.digest')"
  [[ "${digest}" =~ ^sha256:[0-9a-f]{64}$ ]] || { log "❌ no digest for ${tag}"; exit 1; }
  local images image
  images="$(arch_digests "${tag}")"
  for image in "${digest}" ${images}; do
    bash scripts/dev/run.sh bash scripts/release/sign-image.sh "${tag%:*}@${image}" "ctx-${hash}"
  done
  log "✅ ${tag}@${digest}"
  [ -z "${GITHUB_STEP_SUMMARY}" ] || printf '`%s`: `%s@%s`\n' "$(dev_ref_file "${name}")" "${tag}" "${digest}" \
    >> "${GITHUB_STEP_SUMMARY}"
}

# arch_digests TAG: the platform images behind an index (not its build attestation manifests).
function arch_digests() {
  docker buildx imagetools inspect "${1}" --raw | jq -r '.manifests[] | select(.platform.os == "linux") | .digest'
}

# local_login: registry login for Apple `container` with the developer's gh credentials
# (write:packages), only when it holds no credential for the registry yet.
function local_login() {
  local -r host="$(dev_registry | cut -d/ -f1)"
  if container registry list 2> /dev/null | awk 'NR > 1 { print $1 }' | grep -qxF "${host}"; then
    log "✅ ${host}: container already logged in"
    return 0
  fi
  local token user
  token="${GITHUB_TOKEN}"
  [ -n "${token}" ] || token="$(gh auth token)"
  user="$(curl -fsSL --connect-timeout 30 --speed-limit 1024 --speed-time 60 -H "Authorization: Bearer ${token}" https://api.github.com/user | jq -r .login)"
  printf '%s' "${token}" | container registry login "${host}" -u "${user}" --password-stdin
}

# local_image NAME HASH BASE: (macOS) build the multi-arch ctx-HASH index with Apple `container`
# (amd64 under Rosetta) and push it, unless it is already published. The
# dev-image workflow signs it after the commit.
function local_image() {
  local -r name="${1}" hash="${2}" base="${3}"
  local -r tag="$(dev_repository . "${name}"):ctx-${hash}"
  if dev_published_ref "${tag%:*}" "${hash}" > /dev/null; then
    log "✅ ${tag} already published"
  else
    local -a args=(build --platform linux/amd64 --platform linux/arm64 -f "$(dev_containerfile "${name}")" -t "${tag}")
    dev_ensure_builder
    local opt
    while IFS= read -r opt; do args+=("${opt}"); done < <(dev_builder_args)
    [ -z "${base}" ] || args+=(--build-arg "BASE_DEV_IMAGE=${base}")
    log "🔨 ${tag} (linux/amd64, linux/arm64)"
    container "${args[@]}" "$(dev_context_dir "${name}")"
    container image push "${tag}"
  fi
}

main "${@:-}"
