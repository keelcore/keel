#!/usr/bin/env bash
# scripts/release/sign-image.sh
# The one treatment every published container image index gets, tool images and release images alike, each
# step skipped when this repository's workflows already did it:
#   - a keyless cosign signature (Sigstore, the job's GitHub OIDC token);
#   - an SPDX SBOM attestation (scripts/release/generate-sbom.sh, scanned from the registry);
#   - SLSA build provenance (https://slsa.dev/provenance/v1, GitHub's workflow build type), attested with cosign
#     into the registry beside the image, and the same Sigstore bundle posted to GitHub's attestation store
#     (POST /repos/OWNER/REPO/attestations), so both `cosign verify-attestation` and `gh attestation verify`
#     find it.
# Runs in the dev image (cosign, syft, curl, jq), called by scripts/dev/publish.sh merge and
# scripts/release/publish-release-images.sh. The job needs `id-token: write`, `packages: write` and
# `attestations: write`.
#
# Usage: sign-image.sh REPOSITORY@sha256:DIGEST [VERSION]   (VERSION: the SBOM's; default the digest)
# Env: DOCKER_CONFIG (the job's registry login, read-only), else GITHUB_TOKEN and GITHUB_ACTOR (a login of its
# own); GITHUB_TOKEN (GitHub's attestation API); ACTIONS_ID_TOKEN_REQUEST_URL and ACTIONS_ID_TOKEN_REQUEST_TOKEN
# (the signing certificate, and the workflow it names: the provenance's builder); the GitHub Actions run context
# below (the provenance).
# Environment: ACTIONS_ID_TOKEN_REQUEST_TOKEN (default empty).
# Environment: ACTIONS_ID_TOKEN_REQUEST_URL (default empty).
# Environment: DOCKER_CONFIG (default empty).
# Environment: GITHUB_ACTOR (default empty).
# Environment: GITHUB_API_URL (default https://api.github.com).
# Environment: GITHUB_EVENT_NAME (default empty).
# Environment: GITHUB_REF (default empty).
# Environment: GITHUB_REPOSITORY (default empty).
# Environment: GITHUB_REPOSITORY_ID (default empty).
# Environment: GITHUB_REPOSITORY_OWNER_ID (default empty).
# Environment: GITHUB_RUN_ATTEMPT (default empty).
# Environment: GITHUB_RUN_ID (default empty).
# Environment: GITHUB_SERVER_URL (default https://github.com).
# Environment: GITHUB_SHA (default empty).
# Environment: GITHUB_TOKEN (default empty).
# Environment: GITHUB_WORKFLOW_REF (default empty).
# Environment: RUNNER_ENVIRONMENT (default empty).
# Project hook: sign-image.local.sh runs first, when present, with this script's arguments
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

declare -rx ACTIONS_ID_TOKEN_REQUEST_TOKEN="${ACTIONS_ID_TOKEN_REQUEST_TOKEN:-}"
declare -rx ACTIONS_ID_TOKEN_REQUEST_URL="${ACTIONS_ID_TOKEN_REQUEST_URL:-}"
declare -rx DOCKER_CONFIG="${DOCKER_CONFIG:-}"
declare -rx GITHUB_ACTOR="${GITHUB_ACTOR:-}"
declare -rx GITHUB_API_URL="${GITHUB_API_URL:-https://api.github.com}"
declare -rx GITHUB_EVENT_NAME="${GITHUB_EVENT_NAME:-}"
declare -rx GITHUB_REF="${GITHUB_REF:-}"
declare -rx GITHUB_REPOSITORY="${GITHUB_REPOSITORY:-}"
declare -rx GITHUB_REPOSITORY_ID="${GITHUB_REPOSITORY_ID:-}"
declare -rx GITHUB_REPOSITORY_OWNER_ID="${GITHUB_REPOSITORY_OWNER_ID:-}"
declare -rx GITHUB_RUN_ATTEMPT="${GITHUB_RUN_ATTEMPT:-}"
declare -rx GITHUB_RUN_ID="${GITHUB_RUN_ID:-}"
declare -rx GITHUB_SERVER_URL="${GITHUB_SERVER_URL:-https://github.com}"
declare -rx GITHUB_SHA="${GITHUB_SHA:-}"
declare -rx GITHUB_TOKEN="${GITHUB_TOKEN:-}"
declare -rx GITHUB_WORKFLOW_REF="${GITHUB_WORKFLOW_REF:-}"
declare -rx RUNNER_ENVIRONMENT="${RUNNER_ENVIRONMENT:-}"
readonly SBOM_PREDICATE='https://spdx.dev/Document'
readonly PROVENANCE_PREDICATE='https://slsa.dev/provenance/v1'
readonly BUNDLE_TYPE='application/vnd.dev.sigstore.bundle.v0.3+json'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  validate_env
  local -r ref="${1}" version="${2:-${1##*@}}"
  local -ra identity=(--certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
    --certificate-identity-regexp "^https://github.com/${GITHUB_REPOSITORY}/")
  local signed='' sbom='' provenance='' stored=''
  cosign verify "${identity[@]}" "${ref}" > /dev/null 2>&1 && signed=1
  cosign verify-attestation "${identity[@]}" --type "${SBOM_PREDICATE}" "${ref}" > /dev/null 2>&1 && sbom=1
  cosign verify-attestation "${identity[@]}" --type "${PROVENANCE_PREDICATE}" "${ref}" > /dev/null 2>&1 && provenance=1
  github_has_provenance "${ref}" && stored=1
  if [ -n "${signed}" ] && [ -n "${sbom}" ] && [ -n "${provenance}" ] && [ -n "${stored}" ]; then
    log "✅ ${ref} is already signed, with its SBOM and provenance"
    return 0
  fi
  login "${ref}"
  if [ -z "${signed}" ]; then
    cosign sign --yes "${ref}"
    log "✅ signed ${ref}"
  fi
  [ -n "${sbom}" ] || attest_sbom "${ref}" "${version}"
  [ -n "${provenance}" ] || attest_provenance "${ref}"
  [ -n "${stored}" ] || store_provenance "${ref}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keelcore_sign_image.log' >&5
}

function validate_args() {
  if [ "${#}" -lt 1 ] || [ "${#}" -gt 2 ] || [[ ! "${1:-}" =~ ^[^@]+@sha256:[0-9a-f]{64}$ ]]; then
    log '❌ Usage: sign-image.sh REPOSITORY@sha256:DIGEST [VERSION]'
    exit 1
  fi
}

# validate_env: the GitHub Actions run context the signature and the provenance come from.
function validate_env() {
  local name
  for name in GITHUB_REPOSITORY GITHUB_TOKEN GITHUB_WORKFLOW_REF GITHUB_SHA GITHUB_REF GITHUB_RUN_ID GITHUB_RUN_ATTEMPT \
    ACTIONS_ID_TOKEN_REQUEST_URL ACTIONS_ID_TOKEN_REQUEST_TOKEN; do
    [ -n "${!name}" ] && continue
    log "❌ ${name} is not set: sign-image.sh runs in a GitHub Actions job (id-token, packages and attestations: write)"
    exit 1
  done
}

# login REF: the job's one registry login (DOCKER_CONFIG, mounted read-only by run.sh) when there is one;
# a login of its own only without it.
function login() {
  [ -z "${DOCKER_CONFIG}" ] || [ ! -f "${DOCKER_CONFIG}/config.json" ] || return 0
  printf '%s' "${GITHUB_TOKEN}" | cosign login "${1%%/*}" -u "${GITHUB_ACTOR:?GITHUB_ACTOR is required}" --password-stdin
}

# attest_sbom REF VERSION: the index's SPDX SBOM, named after its repository. The full SPDX predicate URI:
# cosign's spdxjson shortname rejects syft's SPDX 2.3 JSON.
function attest_sbom() {
  local -r ref="${1}" version="${2}"
  local -r name="$(basename "${ref%@*}")" dir="$(mktemp -d)"
  bash scripts/release/generate-sbom.sh --out-dir "${dir}" --name "${name}" --version "${version}" "${ref}"
  cosign attest --yes --type "${SBOM_PREDICATE}" --predicate "${dir}/${name}.spdx.json" "${ref}"
  rm -rf "${dir}"
  log "✅ attested the SBOM of ${ref}"
}

# attest_provenance REF: SLSA v1 build provenance for this run, in the predicate GitHub's own
# actions/attest-build-provenance writes (build type https://actions.github.io/buildtypes/workflow/v1): the
# workflow is the run's (a caller, for a called workflow), the builder the job's (builder_id).
function attest_provenance() {
  local -r ref="${1}" predicate="$(mktemp)"
  local -r workflow="${GITHUB_WORKFLOW_REF#"${GITHUB_REPOSITORY}/"}"
  local builder
  builder="$(builder_id)"
  jq -n \
    --arg server "${GITHUB_SERVER_URL}" --arg repo "${GITHUB_REPOSITORY}" \
    --arg path "${workflow%@*}" --arg wref "${workflow#*@}" --arg builder "${builder}" \
    --arg ref "${GITHUB_REF}" --arg sha "${GITHUB_SHA}" --arg event "${GITHUB_EVENT_NAME}" \
    --arg repo_id "${GITHUB_REPOSITORY_ID}" --arg owner_id "${GITHUB_REPOSITORY_OWNER_ID}" \
    --arg runner "${RUNNER_ENVIRONMENT}" --arg run "${GITHUB_RUN_ID}" --arg attempt "${GITHUB_RUN_ATTEMPT}" '{
      buildDefinition: {
        buildType: "https://actions.github.io/buildtypes/workflow/v1",
        externalParameters: {workflow: {ref: $wref, repository: ($server + "/" + $repo), path: $path}},
        internalParameters: {github: {event_name: $event, repository_id: $repo_id,
          repository_owner_id: $owner_id, runner_environment: $runner}},
        resolvedDependencies: [{uri: ("git+" + $server + "/" + $repo + "@" + $ref), digest: {gitCommit: $sha}}]
      },
      runDetails: {
        builder: {id: $builder},
        metadata: {invocationId: ($server + "/" + $repo + "/actions/runs/" + $run + "/attempts/" + $attempt)}
      }
    }' > "${predicate}"
  cosign attest --yes --type "${PROVENANCE_PREDICATE}" --predicate "${predicate}" "${ref}"
  rm -f "${predicate}"
  log "✅ attested the provenance of ${ref}"
}

# github_has_provenance REF: GitHub's attestation store holds provenance for REF's digest.
function github_has_provenance() {
  local count
  count="$(github_api "/repos/${GITHUB_REPOSITORY}/attestations/${1##*@}?predicate_type=provenance" \
    | jq '.attestations | length' 2> /dev/null)" || return 1
  [ "${count:-0}" -gt 0 ]
}

# store_provenance REF: post the provenance bundle the registry holds for REF to GitHub's attestation store,
# which takes only provenance whose builder is the workflow its certificate names: a bundle from another
# builder (a caller's, before builder_id) is attested again first. A refusal fails with GitHub's reason.
function store_provenance() {
  local -r ref="${1}" bundle="$(mktemp)" body="$(mktemp)"
  local builder response
  builder="$(builder_id)"
  registry_provenance_bundle "${ref}" > "${bundle}"
  if [ "$(bundle_builder "${bundle}")" != "${builder}" ]; then
    log "ℹ️  the registry's provenance for ${ref} names another builder; attesting it again"
    attest_provenance "${ref}"
    registry_provenance_bundle "${ref}" > "${bundle}"
  fi
  jq '{bundle: .}' "${bundle}" > "${body}"
  if ! response="$(github_api "/repos/${GITHUB_REPOSITORY}/attestations" -X POST \
    -H 'Content-Type: application/json' --data-binary "@${body}")"; then
    log "❌ GitHub's attestation store refused the provenance of ${ref}: ${response}"
    exit 1
  fi
  rm -f "${bundle}" "${body}"
  log "✅ stored the provenance of ${ref} in GitHub's attestation store"
}

# bundle_builder FILE: the builder a provenance bundle's statement names.
function bundle_builder() {
  jq -r '.dsseEnvelope.payload // empty | @base64d | fromjson | .predicate.runDetails.builder.id // empty' "${1}"
}

# builder_id: the provenance's builder, as GitHub's own names it: the workflow the job runs in (for a called
# workflow, it, not its caller: GITHUB_WORKFLOW_REF), the one the signing certificate names.
function builder_id() {
  local workflow
  workflow="$(job_workflow_ref)" || return 1
  printf '%s/%s' "${GITHUB_SERVER_URL}" "${workflow}"
}

# job_workflow_ref: the job_workflow_ref claim of the job's OIDC token (Sigstore's audience).
function job_workflow_ref() {
  local token claims
  token="$(curl -fsS --connect-timeout 30 --max-time 60 -H "Authorization: Bearer ${ACTIONS_ID_TOKEN_REQUEST_TOKEN}" \
    "${ACTIONS_ID_TOKEN_REQUEST_URL}&audience=sigstore" | jq -r '.value')"
  claims="$(cut -d. -f2 <<< "${token}" | tr '_-' '/+')"
  while [ $(( ${#claims} % 4 )) -ne 0 ]; do
    claims+='='
  done
  base64 -d <<< "${claims}" | jq -er '.job_workflow_ref'
}

# registry_provenance_bundle REF: the newest Sigstore bundle carrying REF's SLSA provenance, read from the
# registry where cosign keeps it: the sha256-<digest> tag (an index of REF's attestations), the provenance
# manifest, its bundle layer.
function registry_provenance_bundle() {
  local -r ref="${1}"
  local -r repository="${ref%@*}" digest="${ref##*@}"
  local -r host="${repository%%/*}" path="${repository#*/}"
  local token manifest layer
  token="$(registry_token "${host}" "${path}")"
  manifest="$(curl -fsS --connect-timeout 30 --max-time 120 -H "Authorization: Bearer ${token}" \
    -H 'Accept: application/vnd.oci.image.index.v1+json' \
    "https://${host}/v2/${path}/manifests/sha256-${digest#sha256:}" \
    | jq -r --arg p "${PROVENANCE_PREDICATE}" \
      '[.manifests[] | select(.annotations["dev.sigstore.bundle.predicateType"] == $p)] | last | .digest // empty')"
  [ -n "${manifest}" ] || { log "❌ no provenance bundle for ${ref} in the registry"; exit 1; }
  layer="$(curl -fsS --connect-timeout 30 --max-time 120 -H "Authorization: Bearer ${token}" \
    -H 'Accept: application/vnd.oci.image.manifest.v1+json' "https://${host}/v2/${path}/manifests/${manifest}" \
    | jq -r '.layers[0].digest')"
  curl -fsSL --connect-timeout 30 --max-time 120 -H "Authorization: Bearer ${token}" \
    "https://${host}/v2/${path}/blobs/${layer}" \
    | jq -e --arg t "${BUNDLE_TYPE}" 'select(.mediaType == $t)'
}

# registry_token HOST PATH: a pull token for PATH, with the job's credentials (the package may be private).
function registry_token() {
  curl -fsS --connect-timeout 30 --max-time 60 -u "${GITHUB_ACTOR:-token}:${GITHUB_TOKEN}" \
    "https://${1}/token?scope=repository:${2}:pull" | jq -r '.token'
}

# github_api PATH [CURL ARG...]: GitHub's REST API with the job's token; a failure prints GitHub's reason.
function github_api() {
  local -r path="${1}"
  shift
  curl --fail-with-body -sS --connect-timeout 30 --max-time 120 -H "Authorization: Bearer ${GITHUB_TOKEN}" \
    -H 'Accept: application/vnd.github+json' -H 'X-GitHub-Api-Version: 2022-11-28' "$@" "${GITHUB_API_URL}${path}"
}

main "${@:-}"
