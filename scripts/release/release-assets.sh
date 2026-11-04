#!/usr/bin/env bash
# scripts/release/release-assets.sh
# Assemble every asset of the GitHub Release for TAG in DIR, from what the
# registries serve for that version (so the SBOMs, checksums and signatures cover
# exactly what was published, and the step reruns for any tag):
#   - the npm tarball (package.json), checked against the registry's integrity hash
#   - the wheel and sdist (pyproject.toml), each checked against PyPI's sha256
#   - the Go module zip (go.mod), checked against the Go checksum database (go mod download)
#   - the project's own artifacts (binaries, charts, ...): the project hook
#     scripts/release/release-assets.local.sh TAG DIR places them in DIR and prints their paths
#   - each artifact's provenance, as published: npm's attestations (*.npm-attestations.json),
#     PyPI's provenance (*.pypi-provenance.json), GitHub's build attestations by digest
#     (*.github-attestations.json) and the checksum database record (*.zip.sumdb.txt)
#   - one SBOM per published artifact (scripts/release/sbom.sh): name, version, contents
#   - one build-environment SBOM per tool image the release was built in (TAG's
#     containers/*/image.ref, by digest), completed with the pinned tools syft cannot identify
#   - current_pins.md: every pinned input of TAG (its docs/current_pins.md)
#   - LICENSE: the repository's license file at TAG
#   - SHA256SUMS over all of the above, and a keyless cosign bundle per file
#     (scripts/release/sign.sh)
#
# Usage: release-assets.sh TAG DIR   (in the dev image: make release-assets TAG=vX.Y.Z)
# Env: ACTIONS_ID_TOKEN_REQUEST_URL / _TOKEN (keyless signing in GitHub Actions); GITHUB_REPOSITORY
# (owner/repo: its build attestations); GITHUB_TOKEN (optional: the attestation API's rate limit).
# RELEASE_LICENSE: the SPDX license id for the SBOMs when package.json / pyproject.toml do not name one.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# A newly published version can take minutes to appear in a registry.
readonly FETCH_TRIES=30
readonly FETCH_DELAY=30
readonly LOCAL_HOOK='scripts/release/release-assets.local.sh'

function main() {
  validate_args "${@:-}"
  local -r tag="${1}" dir="${2}" root="${PWD}"
  local -r version="${tag#v}"
  local name license
  name="$(project_name "${tag}")"
  license="${RELEASE_LICENSE:-$(project_license "${tag}")}"
  mkdir -p "${dir}"
  local -a artifacts=()
  local f
  while IFS= read -r f; do
    [ -n "${f}" ] && artifacts+=("${f}")
  done < <(fetch_npm "${tag}" "${dir}"; fetch_pypi "${tag}" "${dir}"; fetch_go_module "${tag}" "${dir}"; local_artifacts "${tag}" "${dir}")
  [ "${#artifacts[@]}" -gt 0 ] || { log "❌ no published artifacts found for ${tag}"; exit 1; }
  bash scripts/release/sbom.sh --out-dir "${dir}" --name "${name}" --version "${version}" \
    --license "${license}" "${artifacts[@]}"
  for f in "${artifacts[@]}"; do
    fetch_github_attestations "${f}"
  done
  local ref_file image tools
  while IFS= read -r ref_file; do
    [ -n "${ref_file}" ] || continue
    image="$(basename "$(dirname "${ref_file}")")"
    tools="$(mktemp)"
    image_tools "${tag}" "${image}" > "${tools}"
    bash scripts/release/sbom.sh --out-dir "${dir}" --name "${name}-build-environment" --version "${version}" \
      --tools "${tools}" "$(git show "${tag}:${ref_file}")"
    rm -f "${tools}"
  done < <(git ls-tree -r --name-only "${tag}" -- containers | grep -E '/image\.ref$' || true)
  if git cat-file -e "${tag}:docs/current_pins.md" 2> /dev/null; then
    git show "${tag}:docs/current_pins.md" > "${dir}/current_pins.md"
  fi
  if git cat-file -e "${tag}:LICENSE" 2> /dev/null; then
    git show "${tag}:LICENSE" > "${dir}/LICENSE"
  fi
  local -a files=()
  for f in "${dir}"/*; do
    [ -f "${f}" ] || continue
    case "$(basename "${f}")" in
      *.bundle | SHA256SUMS*) continue ;;
    esac
    files+=("$(basename "${f}")")
  done
  (cd "${dir}" && bash "${root}/scripts/release/sign.sh" --sums-dir . "${files[@]}")
  log "✅ release assets for ${tag}: $(find "${dir}" -maxdepth 1 -type f | wc -l | tr -d ' ') files in ${dir}"
}

# local_artifacts TAG DIR: the project's own artifacts, from its hook (none without one).
function local_artifacts() {
  [ -f "${LOCAL_HOOK}" ] || return 0
  bash "${LOCAL_HOOK}" "${1}" "${2}"
}

# log MSG: to stderr (stdout carries the fetched artifact paths).
function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/release_assets.log' >&2
}

function validate_args() {
  if [ "${#}" -ne 2 ] || [[ ! "${1:-}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    log '❌ Usage: release-assets.sh vX.Y.Z DIR'
    exit 1
  fi
  git rev-parse --verify --quiet "refs/tags/${1}" > /dev/null || { log "❌ tag ${1} not found"; exit 1; }
}

# tag_file TAG PATH: PATH's content at TAG (empty when it does not exist there).
function tag_file() {
  git show "${1}:${2}" 2> /dev/null || true
}

# project_name TAG: the published package name (package.json, else pyproject.toml, else the repo).
function project_name() {
  local -r tag="${1}"
  local pkg
  pkg="$(tag_file "${tag}" package.json)"
  if [ -n "${pkg}" ]; then
    jq -r '.name | sub("^@"; "") | gsub("/"; "-")' <<< "${pkg}"
  elif [ -n "$(tag_file "${tag}" pyproject.toml)" ]; then
    tag_file "${tag}" pyproject.toml | sed -nE 's/^name = "([^"]+)"$/\1/p' | head -1
  else
    basename "$(git rev-parse --show-toplevel)"
  fi
}

# project_license TAG: the SPDX license the project declares (package.json, else pyproject.toml).
function project_license() {
  local -r tag="${1}"
  local pkg
  pkg="$(tag_file "${tag}" package.json)"
  if [ -n "${pkg}" ]; then
    jq -r '.license // empty' <<< "${pkg}"
  else
    tag_file "${tag}" pyproject.toml | sed -nE 's/^license = \{ text = "([^"]+)" \}$/\1/p' | head -1
  fi
}

# retry_fetch URL: the response body, retried while the registry does not have it yet.
function retry_fetch() {
  local -r url="${1}"
  local i
  for (( i = 1; i <= FETCH_TRIES; i++ )); do
    curl -fsSL "${url}" && return 0
    log "⏳ ${url} not available yet (${i}/${FETCH_TRIES})"
    sleep "${FETCH_DELAY}"
  done
  return 1
}

# fetch_npm TAG DIR: the npm tarball for TAG's version, checked against the registry's
# integrity hash; prints its path.
function fetch_npm() {
  local -r tag="${1}" dir="${2}"
  local pkg name meta url integrity file
  pkg="$(tag_file "${tag}" package.json)"
  [ -n "${pkg}" ] || return 0
  name="$(jq -r '.name' <<< "${pkg}")"
  meta="$(retry_fetch "https://registry.npmjs.org/${name/\//%2f}/${tag#v}")" \
    || { log "❌ npm ${name}@${tag#v} not found"; exit 1; }
  url="$(jq -r '.dist.tarball' <<< "${meta}")"
  integrity="$(jq -r '.dist.integrity' <<< "${meta}")"
  file="${dir}/$(basename "${url}")"
  curl -fsSL "${url}" -o "${file}"
  [ "sha512-$(openssl dgst -sha512 -binary "${file}" | base64 | tr -d '\n')" = "${integrity}" ] \
    || { log "❌ ${file}: integrity mismatch with the npm registry"; exit 1; }
  # npm's provenance and publish attestations for this version.
  curl -fsSL "$(jq -r '.dist.attestations.url' <<< "${meta}")" -o "${file}.npm-attestations.json"
  log "✅ npm ${name}@${tag#v}"
  printf '%s\n' "${file}"
}

# fetch_pypi TAG DIR: every file PyPI serves for TAG's version, each checked against its
# published sha256; prints their paths.
function fetch_pypi() {
  local -r tag="${1}" dir="${2}"
  local name meta fn url sha
  [ -n "$(tag_file "${tag}" pyproject.toml)" ] || return 0
  name="$(tag_file "${tag}" pyproject.toml | sed -nE 's/^name = "([^"]+)"$/\1/p' | head -1)"
  meta="$(retry_fetch "https://pypi.org/pypi/${name}/${tag#v}/json")" \
    || { log "❌ PyPI ${name} ${tag#v} not found"; exit 1; }
  while IFS=$'\t' read -r fn url sha; do
    [ -n "${fn}" ] || continue
    curl -fsSL "${url}" -o "${dir}/${fn}"
    printf '%s  %s\n' "${sha}" "${dir}/${fn}" | sha256sum --check --quiet \
      || { log "❌ ${fn}: sha256 mismatch with PyPI"; exit 1; }
    # PyPI's provenance (trusted-publisher attestations) for this file.
    curl -fsSL -H 'Accept: application/vnd.pypi.integrity.v1+json' \
      "https://pypi.org/integrity/${name}/${tag#v}/${fn}/provenance" -o "${dir}/${fn}.pypi-provenance.json"
    log "✅ PyPI ${fn}"
    printf '%s\n' "${dir}/${fn}"
  done < <(jq -r '.urls[] | [.filename, .url, .digests.sha256] | @tsv' <<< "${meta}")
}

# fetch_go_module TAG DIR: the module zip for TAG, as `go get` downloads and verifies it (the Go
# proxy, checked against the checksum database); prints its path.
function fetch_go_module() {
  local -r tag="${1}" dir="${2}"
  local gomod module tmp zip i
  gomod="$(tag_file "${tag}" go.mod)"
  [ -n "${gomod}" ] || return 0
  module="$(awk '$1 == "module" { print $2; exit }' <<< "${gomod}")"
  tmp="$(mktemp -d)"
  for (( i = 1; i <= FETCH_TRIES; i++ )); do
    zip="$(cd "${tmp}" && GOMODCACHE="${tmp}/mod" GOFLAGS=-modcacherw go mod download -json "${module}@${tag}" \
      2> /dev/null | jq -r '.Zip // empty')" && [ -n "${zip}" ] && break
    log "⏳ Go proxy ${module}@${tag} not available yet (${i}/${FETCH_TRIES})"
    sleep "${FETCH_DELAY}"
  done
  [ -n "${zip:-}" ] || { log "❌ Go proxy ${module}@${tag} not found"; exit 1; }
  cp "${zip}" "${dir}/$(basename "${module}")-${tag}.zip"
  # The checksum database's signed record of this module version.
  curl -fsSL "https://sum.golang.org/lookup/${module}@${tag}" -o "${dir}/$(basename "${module}")-${tag}.zip.sumdb.txt"
  rm -rf "${tmp}"
  log "✅ Go module ${module}@${tag} (checksum database verified)"
  printf '%s\n' "${dir}/$(basename "${module}")-${tag}.zip"
}

# fetch_github_attestations FILE: the build attestations GitHub holds for FILE's digest
# (actions/attest-build-provenance), saved beside it when there are any.
function fetch_github_attestations() {
  local -r file="${1}"
  local digest body
  digest="$(sha256sum "${file}" | cut -d' ' -f1)"
  local -a auth=()
  [ -z "${GITHUB_TOKEN:-}" ] || auth=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
  body="$(curl -fsSL ${auth[@]+"${auth[@]}"} -H 'Accept: application/vnd.github+json' \
    "https://api.github.com/repos/${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}/attestations/sha256:${digest}" \
    2> /dev/null || true)"
  [ -n "${body}" ] && [ "$(jq '.attestations // [] | length' <<< "${body}")" -gt 0 ] || return 0
  printf '%s\n' "${body}" > "${file}.github-attestations.json"
  log "✅ GitHub attestations for $(basename "${file}")"
}

# image_tools TAG IMAGE: name, version, purl and release URL (TSV) of every tool TAG's
# containers/IMAGE/Containerfile installs with a pinned setup script that downloads a GitHub
# release or clones a release tag — the rows sbom.sh --tools adds when syft cannot identify them.
function image_tools() {
  local -r tag="${1}" image="${2}"
  local script content version repo tool v
  while IFS= read -r script; do
    content="$(tag_file "${tag}" "${script}")"
    version="$(sed -nE "s/^readonly [A-Z0-9_]+_VERSION='([^']+)'\$/\\1/p" <<< "${content}" | head -1 || true)"
    repo="$(grep -oE 'github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_-]+(/releases/download|\.git)' <<< "${content}" \
      | head -1 | cut -d/ -f2-3 | sed 's/\.git$//' || true)"
    [ -n "${version}" ] && [ -n "${repo}" ] || continue
    tool="$(basename "${script}" .sh)"
    tool="${tool#setup-}"
    # The release tag as the download URL spells it (v-prefixed for most tools; staticcheck: 2026.2.2).
    if grep -qE 'v\$\{[A-Z0-9_]+_VERSION\}' <<< "${content}"; then v="v${version#v}"; else v="${version}"; fi
    printf '%s\t%s\t%s\t%s\n' "${tool}" "${version#v}" "pkg:github/${repo}@${v}" "https://github.com/${repo}/releases/tag/${v}"
  done < <(tag_file "${tag}" "containers/${image}/Containerfile" \
    | grep -oE 'scripts/ci/setup-[a-z0-9-]+\.sh' | sort -u)
}

main "${@:-}"
