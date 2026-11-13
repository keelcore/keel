#!/usr/bin/env bash
# scripts/release/clean-github-artifacts.sh
# Deletes what a repository published on GitHub from any commit but HEAD and its release tag: workflow runs
# (with their logs and artifacts), Actions artifacts and caches, deployments, releases (their git tags stay),
# package versions, and the attestations of the images and release assets it deletes. A kept image keeps every
# manifest it references: its platform images, build attestations, signatures and referrers. The tool images
# HEAD names in *image.ref are kept too. Git refs and repository settings are read, never written.
#
# The repository is GITROOT, or else the nearest directory at or above the working directory that holds a .git
# directory; a .git file (a submodule or worktree) is passed over, so run from .standards/ it is the consumer.
#
# It inventories first and prints what it keeps and deletes, then asks for the repository name to be typed
# before deleting; without a terminal it stops there (a dry run). After deleting it inventories again and
# fails unless nothing stale is left, the kept release and images are intact and the tags are unchanged.
#
# Usage: clean-github-artifacts.sh (no arguments: make clean-github-artifacts)
# Env: GITROOT (default empty), the repository, else found from the working directory; GITHUB_TOKEN (default
# empty), else gh's own login; GITHUB_CLEAN_CONFIRM (default empty), the repository name to confirm without asking
# at the terminal; GITHUB_CLEAN_BACKOFF (default 60), seconds per retry of a rate-limited call.
# Needs gh (logged in to the repository's owner), git, curl and jq; the token needs the repo, read:packages
# and delete:packages scopes.
# Project hook: clean-github-artifacts.local.sh runs first, when present (scripts/lib/local-hook.sh).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/local-hook.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/local-hook.sh"

declare -rx GITROOT="${GITROOT:-}"
declare -rx GITHUB_TOKEN="${GITHUB_TOKEN:-}"
declare -rx GITHUB_CLEAN_CONFIRM="${GITHUB_CLEAN_CONFIRM:-}"
declare -rx GITHUB_CLEAN_BACKOFF="${GITHUB_CLEAN_BACKOFF:-60}"

declare -r CLEAN_DIR="${TMPDIR:-/tmp}"'/clean-github-artifacts'
declare -r INV="${CLEAN_DIR}/inventory"
declare -r LOG_FILE='/tmp/clean-github-artifacts.log'
# Deletion order: a deployment, release or image before the runs that made it.
declare -r CATEGORIES='deployments releases packages attestations artifacts caches runs'
declare -r PACKAGE_TYPES='container npm maven rubygems nuget'
declare -r MANIFEST_TYPES='application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json'
# The keep set: the commits HEAD and its release tag, and the image tags they publish ($refs: the tool image
# tags HEAD names in *image.ref).
declare -r CONTEXT_FILTER='{
  repo: $repo,
  owner: ($repo | split("/")[0]),
  owner_path: (if $type == "Organization" then "orgs" else "users" end),
  branch: $branch, head: $head, tag: $tag, tag_sha: $tag_sha, login: $login,
  keep_shas: ([$head, $tag_sha] | unique),
  keep_tags: ([$tag, ($tag | ltrimstr("v")), $head, $head[0:7], $tag_sha, $tag_sha[0:7]] + $refs | unique),
  keep_tag_prefixes: ([$tag + "-", ($tag | ltrimstr("v")) + "-"] | unique)
}'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  prepare
  inventory
  summarize
  nothing_stale && return 0
  confirmed || return 0
  delete_stale
  verify
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a "${LOG_FILE}" >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument (usage: clean-github-artifacts.sh)'
    exit 1
  fi
}

function require_tools() {
  local tool
  for tool in gh git curl jq; do
    command -v "${tool}" > /dev/null || { log "❌ ${tool} is required (brew install ${tool})"; exit 1; }
  done
}

function prepare() {
  require_tools
  enter_repo
  reset_dir
  log "🔎 Cleaning stale published artifacts: log ${LOG_FILE}, lists in ${INV}"
  check_scopes
  write_context
  git ls-remote --tags origin > "${CLEAN_DIR}/tags.before"
  log "🔒 Keeping what $(ctx tag) ($(ctx tag_sha)) and HEAD ($(ctx head)) published in $(ctx repo)"
}

function reset_dir() {
  umask 077
  rm -rf "${CLEAN_DIR}"
  mkdir -p "${INV}"
}

# --- repository, identity and keep set -------------------------------------------------------------------------

function enter_repo() {
  local root
  root="$(repo_root)"
  cd "${root}"
  log "📁 Repository: ${root}"
}

# repo_root: GITROOT, or the nearest directory at or above the working directory with a .git directory.
function repo_root() {
  local dir
  [ -z "${GITROOT}" ] || { require_dir "${GITROOT}"; printf '%s' "${GITROOT}"; return 0; }
  dir="${PWD}"
  until [ -d "${dir}/.git" ]; do
    [ "${dir}" != '/' ] || { log "❌ No .git directory at or above ${PWD}: run in a repository, or set GITROOT"; exit 1; }
    dir="$(dirname "${dir}")"
  done
  printf '%s' "${dir}"
}

function require_dir() {
  [ -d "${1}" ] && return 0
  log "❌ GITROOT ${1} is not a directory"
  exit 1
}

# check_scopes: a classic token must carry the scopes the clean-up needs; a fine-grained or app token reports none,
# and GitHub checks each call.
function check_scopes() {
  local scopes
  scopes="$(gh api -i user | tr -d '\r' | awk -F ': ' 'tolower($1) == "x-oauth-scopes" { print $2 }')"
  if [ -z "${scopes}" ]; then
    log '⚠️  The token reports no OAuth scopes (fine-grained or app token): GitHub checks each call'
    return 0
  fi
  require_scopes "${scopes}"
}

function require_scopes() {
  local missing=''
  has_scope "${1}" 'repo' || missing="${missing} repo"
  has_scope "${1}" 'read:packages' || has_scope "${1}" 'write:packages' || missing="${missing} read:packages"
  has_scope "${1}" 'delete:packages' || missing="${missing} delete:packages"
  if [ -n "${missing}" ]; then
    log "❌ The token lacks the scopes:${missing} (gh auth refresh -s $(tr ' ' ',' <<< "${missing# }"))"
    exit 1
  fi
  log "✅ Token scopes: ${1}"
}

function has_scope() {
  [[ ", ${1}, " == *", ${2}, "* ]]
}

function write_context() {
  local repo branch head tag
  repo="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
  branch="$(gh repo view --json defaultBranchRef --jq .defaultBranchRef.name)"
  head="$(git rev-parse HEAD)"
  require_on_branch "${head}" "${branch}"
  tag="$(current_tag)"
  context "${repo}" "${branch}" "${head}" "${tag}" > "${CLEAN_DIR}/context.json"
}

# context REPO BRANCH HEAD TAG: the keep set as JSON (CONTEXT_FILTER).
function context() {
  local tag_sha type login refs
  tag_sha="$(git rev-list -n 1 "${4}")"
  type="$(gh api "users/${1%%/*}" --jq .type)"
  login="$(gh api user --jq .login)"
  refs="$(image_ref_tags)"
  jq -n --arg repo "${1}" --arg branch "${2}" --arg head "${3}" --arg tag "${4}" --arg tag_sha "${tag_sha}" \
    --arg type "${type}" --arg login "${login}" --argjson refs "${refs}" \
    "${CONTEXT_FILTER}"
}

function require_on_branch() {
  local remote
  remote="$(git ls-remote origin "refs/heads/${2}" | cut -f 1)"
  if ! git merge-base --is-ancestor "${1}" "${remote}" 2> /dev/null; then
    log "❌ HEAD ${1} is not on origin/${2}: check out the published ${2} (git fetch first)"
    exit 1
  fi
}

# current_tag: the highest tag at HEAD, else the nearest tag before it; it must be on origin.
function current_tag() {
  local tag
  tag="$(git tag --points-at HEAD --sort=-v:refname | head -n 1)"
  [ -n "${tag}" ] || tag="$(git describe --tags --abbrev=0 2> /dev/null || true)"
  [ -n "${tag}" ] || { log '❌ No release tag at or before HEAD'; exit 1; }
  if ! git ls-remote --exit-code origin "refs/tags/${tag}" > /dev/null; then
    log "❌ The tag ${tag} is not on origin"
    exit 1
  fi
  printf '%s' "${tag}"
}

# image_ref_tags: the tags of the tool images HEAD names, as a JSON array.
function image_ref_tags() {
  local file
  git ls-files '*image.ref' | while IFS= read -r file; do
    sed -E 's/.*://' "${file}"
  done | jq -R . | jq -sc .
}

function ctx() {
  jq -r ".${1}" "${CLEAN_DIR}/context.json"
}

function ctx_list() {
  jq -r ".${1}[]" "${CLEAN_DIR}/context.json"
}

function lower() {
  awk '{ print tolower($0) }'
}

function token() {
  if [ -n "${GITHUB_TOKEN}" ]; then
    printf '%s' "${GITHUB_TOKEN}"
  else
    gh auth token
  fi
}

# --- inventory ------------------------------------------------------------------------------------------------
# Each category writes INV/<category>.all (every item, JSON), .keep and .delete (tab-separated: the DELETE
# endpoint, or the kept item, and a label).

function inventory() {
  log '🔎 Inventorying workflow runs, artifacts, caches, deployments, releases, packages and attestations'
  inventory_actions
  inventory_repo
  inventory_packages
  inventory_attestations
}

function inventory_actions() {
  local repo
  repo="$(ctx repo)"
  list runs "repos/${repo}/actions/runs" '.workflow_runs[]' '.head_sha | IN($c.keep_shas[])' \
    '"\(.name) #\(.run_number) \(.head_sha[0:7])"'
  list artifacts "repos/${repo}/actions/artifacts" '.artifacts[]' '.workflow_run.head_sha | IN($c.keep_shas[])' \
    '"\(.name) \(.workflow_run.head_sha[0:7])"'
  list caches "repos/${repo}/actions/caches" '.actions_caches[]' \
    '.ref | IN("refs/heads/\($c.branch)", "refs/tags/\($c.tag)")' '"\(.key) \(.ref)"'
}

function inventory_repo() {
  local repo
  repo="$(ctx repo)"
  list deployments "repos/${repo}/deployments" '.[]' '.sha | IN($c.keep_shas[])' '"\(.environment) \(.sha[0:7])"'
  list releases "repos/${repo}/releases" '.[]' '.tag_name == $c.tag' \
    '"\(.tag_name)\(if .draft then " (draft)" else "" end)"'
}

# list CATEGORY ENDPOINT ITEMS KEEP LABEL: the items ENDPOINT lists (the jq filter ITEMS), split by the jq
# predicate KEEP ($c is the keep set); an item is deleted at ENDPOINT/<id>.
function list() {
  local -r filter="\$ctx[0] as \$c | [(${4}), \"\(\$ep)/\(.id)\", (${5})] | @tsv"
  gh api --paginate "${2}?per_page=100" --jq "${3}" > "${INV}/${1}.all"
  jq -r --slurpfile ctx "${CLEAN_DIR}/context.json" --arg ep "${2}" "${filter}" "${INV}/${1}.all" \
    > "${INV}/${1}.tsv"
  awk -F '\t' -v OFS='\t' '$1 == "true" { print $2, $3 }' "${INV}/${1}.tsv" > "${INV}/${1}.keep"
  awk -F '\t' -v OFS='\t' '$1 != "true" { print $2, $3 }' "${INV}/${1}.tsv" > "${INV}/${1}.delete"
}

# --- packages -------------------------------------------------------------------------------------------------

function inventory_packages() {
  local type name
  : > "${INV}/packages.all"
  : > "${INV}/packages.keep"
  : > "${INV}/packages.delete"
  : > "${INV}/deleted-digests"
  : > "${INV}/kept-images"
  for type in ${PACKAGE_TYPES}; do
    for name in $(repo_packages "${type}"); do
      inventory_package "${type}" "${name}"
    done
  done
}

# repo_packages TYPE: the owner's packages of TYPE that belong to this repository.
function repo_packages() {
  gh api --paginate "$(ctx owner_path)/$(ctx owner)/packages?package_type=${1}&per_page=100" \
    --jq ".[] | select(.repository.full_name == \"$(ctx repo)\") | .name"
}

# inventory_package TYPE NAME: VERSIONS holds the package's versions, VERSIONS.kept the names of those kept.
function inventory_package() {
  local api versions
  api="$(ctx owner_path)/$(ctx owner)/packages/${1}/${2//\//%2F}"
  versions="${INV}/versions-${1}-${2//\//_}"
  tick "packages: ${1}/${2}"
  gh api --paginate "${api}/versions?per_page=100" --jq '.[]' | jq -c --arg p "${1}/${2}" '. + {package: $p}' \
    > "${versions}"
  cat "${versions}" >> "${INV}/packages.all"
  kept_versions "${1}" "${2}" "${versions}"
  package_lists "${1}" "${2}" "${api}" "${versions}"
}

# kept_versions TYPE NAME VERSIONS: an image is kept by a kept tag, then with everything a kept manifest
# references; another package's version by its name.
function kept_versions() {
  local -r tagged='any(.metadata.container.tags[]?; IN($c.keep_tags[]) or (. as $t | any($c.keep_tag_prefixes[]; . as $p | $t | startswith($p))))'
  local filter='.name | IN($c.keep_tags[])'
  [ "${1}" != 'container' ] || filter="${tagged}"
  jq -r --slurpfile ctx "${CLEAN_DIR}/context.json" "\$ctx[0] as \$c | select(${filter}) | .name" "${3}" \
    > "${3}.kept"
  [ "${1}" != 'container' ] || close_references "${2}" "${3}"
}

# close_references NAME VERSIONS: adds to VERSIONS.kept every version a kept one references, until none is new.
function close_references() {
  local line=1 digest refs ref
  while digest="$(sed -n "${line}p" "${2}.kept")" && [ -n "${digest}" ]; do
    refs="$(references "${1}" "${digest}" "${2}")"
    for ref in ${refs}; do
      grep -qxF "${ref}" "${2}.kept" || printf '%s\n' "${ref}" >> "${2}.kept"
    done
    line=$(( line + 1 ))
  done
}

# references NAME DIGEST VERSIONS: the versions of the image NAME that DIGEST references: an index's manifests
# (platform images, build attestations), its OCI referrers, and the sha256-<hex> tags of signatures,
# attestations and SBOMs that name it. An unreadable kept manifest stops the clean-up: what it references is
# unknown, and deleting it would break the kept image.
function references() {
  local body
  if ! body="$(manifest "${1}" "${2}")"; then
    log "❌ Cannot read the kept manifest ${1}@${2}: stopping, nothing deleted"
    exit 1
  fi
  {
    jq -r '.manifests[]?.digest' <<< "${body}"
    referrers "${1}" "${2}" | jq -r '.manifests[]?.digest' 2> /dev/null || true
    tagged_for "${2}" "${3}"
  } | grep -xF -f <(jq -r '.name' "${3}") || true
}

# tagged_for DIGEST VERSIONS: the versions tagged sha256-<hex of DIGEST>, or that tag with a suffix (.sig, .att).
function tagged_for() {
  jq -r --arg t "sha256-${1#sha256:}" \
    'select(any(.metadata.container.tags[]?; . == $t or startswith($t + "."))) | .name' "${2}"
}

# package_lists TYPE NAME API VERSIONS: a package with no kept version is deleted whole (GitHub keeps a
# package's last version); otherwise each stale version is.
function package_lists() {
  jq -r --arg p "${1}/${2}" --rawfile kept "${4}.kept" \
    'select(.name as $n | $kept | split("\n") | index($n)) | [$p, .name, (.metadata.container.tags // [] | join(","))] | @tsv' \
    "${4}" >> "${INV}/packages.keep"
  [ "${1}" != 'container' ] || sed "s#^#${2}\t#" "${4}.kept" >> "${INV}/kept-images"
  stale_versions "${4}" > "${4}.stale"
  [ "${1}" != 'container' ] || jq -r '.name' "${4}.stale" >> "${INV}/deleted-digests"
  if [ -s "${4}.kept" ]; then
    jq -r --arg api "${3}" --arg p "${1}/${2}" \
      '["\($api)/versions/\(.id)", "\($p) \(.name) \(.metadata.container.tags // [] | join(","))"] | @tsv' \
      "${4}.stale" >> "${INV}/packages.delete"
  elif [ -s "${4}.stale" ]; then
    printf '%s\t%s/%s: the whole package (%s versions)\n' "${3}" "${1}" "${2}" "$(jq -s length "${4}")" \
      >> "${INV}/packages.delete"
  fi
}

function stale_versions() {
  jq -c --rawfile kept "${1}.kept" 'select(.name as $n | $kept | split("\n") | index($n) | not)' "${1}"
}

# --- attestations ---------------------------------------------------------------------------------------------

# inventory_attestations: the attestations of each deleted image and release asset that nothing kept shares.
function inventory_attestations() {
  local digest
  : > "${INV}/attestations.all"
  : > "${INV}/attestations.keep"
  : > "${INV}/attestations.delete"
  for digest in $(attestation_subjects); do
    tick "attestations: ${digest}"
    [ "$(attestation_count "${digest}")" -gt 0 ] || continue
    jq -nc --arg d "${digest}" '$d' >> "${INV}/attestations.all"
    printf '%s/%s/attestations/digest/%s\tattestations of %s\n' "$(ctx owner_path)" "$(ctx owner)" \
      "${digest}" "${digest}" >> "${INV}/attestations.delete"
  done
}

function attestation_subjects() {
  { cat "${INV}/deleted-digests"; release_digests false; } | awk 'NF && !seen[$0]++' \
    | grep -vxF -f <(kept_digests) || true
}

function kept_digests() {
  cut -f 2 "${INV}/kept-images"
  release_digests true
}

# release_digests KEPT: the asset digests of the kept (true) or deleted (false) releases.
function release_digests() {
  jq -r --slurpfile ctx "${CLEAN_DIR}/context.json" --argjson kept "${1}" \
    '$ctx[0] as $c | select((.tag_name == $c.tag) == $kept) | .assets[]?.digest // empty' "${INV}/releases.all"
}

# attestation_count DIGEST: GitHub answers 404 for a digest without attestations; gh prints that body too.
function attestation_count() {
  local count
  count="$(gh api "repos/$(ctx repo)/attestations/${1}" --jq '.attestations | length' 2> /dev/null)" || count=0
  printf '%s' "${count}"
}

# --- summary and confirmation ---------------------------------------------------------------------------------

function summarize() {
  local category
  log '| category | total | keep | delete |'
  log '| --- | --- | --- | --- |'
  for category in ${CATEGORIES}; do
    log "| ${category} | $(jq -s length "${INV}/${category}.all") | $(lines "${category}.keep") | $(lines "${category}.delete") |"
  done
  log_kept
  report_only
}

function lines() {
  wc -l < "${INV}/${1}" | tr -d ' '
}

function log_kept() {
  local line
  log "🔒 Kept releases: $(cut -f 2 "${INV}/releases.keep" | paste -sd ' ' -)"
  log '🔒 Kept package versions:'
  while IFS= read -r line; do
    log "   ${line}"
  done < "${INV}/packages.keep"
  log "📄 Every list: ${INV}/<category>.keep and .delete"
}

# report_only: what stays published from other commits, which this clean-up leaves alone. A failed call (no code
# scanning, no Pages site) reports none; gh prints the error body on stdout, so it is dropped.
function report_only() {
  local repo shas pages
  repo="$(ctx repo)"
  shas="$(gh api --paginate "repos/${repo}/code-scanning/analyses?per_page=100" --jq '.[].commit_sha' 2> /dev/null)" \
    || shas=''
  log "ℹ️  Left in place: $(printf '%s' "${shas}" | grep -cvxF -f <(ctx_list keep_shas) || true) code-scanning analyses of other commits"
  pages="$(gh api "repos/${repo}/pages" --jq .html_url 2> /dev/null)" || pages=''
  [ -z "${pages}" ] || log "ℹ️  Left in place: the Pages site ${pages}"
}

function nothing_stale() {
  [ "$(cat "${INV}"/*.delete | wc -l | tr -d ' ')" -eq 0 ] || return 1
  log '✅ Nothing stale is published'
}

function confirmed() {
  local answer
  answer="${GITHUB_CLEAN_CONFIRM}"
  if [ -z "${answer}" ] && [ ! -t 0 ]; then
    log '🛑 Nothing deleted: dry run without a terminal (run make clean-github-artifacts at one to delete)'
    return 1
  fi
  [ -n "${answer}" ] || answer="$(ask)"
  [ "${answer}" = "$(ctx repo)" ] || { log '🛑 Nothing deleted: not confirmed'; return 1; }
  log "⚠️  Confirmed: deleting from $(ctx repo)"
}

function ask() {
  local answer=''
  printf '⚠️  Type %s to delete everything listed for deletion above: ' "$(ctx repo)" >&5
  read -r answer || true
  printf '%s' "${answer}"
}

# --- deletion -------------------------------------------------------------------------------------------------

function delete_stale() {
  local category
  for category in ${CATEGORIES}; do
    delete_category "${category}"
  done
}

# delete_category CATEGORY: deletes each item of CATEGORY.delete; a failure is logged, kept in
# CATEGORY.failed, and the rest go on.
function delete_category() {
  local endpoint label count=0 total
  total="$(lines "${1}.delete")"
  log "🗑️  ${1}: deleting ${total}"
  while IFS=$'\t' read -r -u 3 endpoint label; do
    delete_one "${1}" "${endpoint}" || printf '%s\t%s\n' "${endpoint}" "${label}" >> "${INV}/${1}.failed"
    count=$(( count + 1 ))
    tick "${1}: ${count}/${total} deleted"
  done 3< "${INV}/${1}.delete"
}

# delete_one CATEGORY ENDPOINT: GitHub deletes only an inactive deployment.
function delete_one() {
  if [ "${1}" = 'deployments' ]; then
    call POST "${2}/statuses" -f state=inactive || return 1
  fi
  call DELETE "${2}"
}

# call METHOD ENDPOINT [ARGS]: one API write. Already gone (404) is done; a rate limit is waited out and retried.
function call() {
  local attempt err
  for attempt in 1 2 3 4 5; do
    err="$(gh api -X "${1}" "${2}" "${@:3}" 2>&1 > /dev/null)" && return 0
    case "${err}" in
      *'(HTTP 404)'*) return 0 ;;
      *'(HTTP 429)'* | *'rate limit'*) backoff "${attempt}" "${2}" ;;
      *) log "❌ ${1} ${2}: ${err}"; return 1 ;;
    esac
  done
  log "❌ ${1} ${2}: still rate limited"
  return 1
}

function backoff() {
  log "⏳ Rate limited on ${2}: waiting $(( GITHUB_CLEAN_BACKOFF * ${1} ))s"
  sleep "$(( GITHUB_CLEAN_BACKOFF * ${1} ))"
}

# tick MESSAGE: a progress line at most every 30 seconds.
function tick() {
  local now last
  now="$(date -u +%s)"
  last="$(cat "${CLEAN_DIR}/tick" 2> /dev/null || printf '0')"
  [ $(( now - last )) -ge 30 ] || return 0
  printf '%s' "${now}" > "${CLEAN_DIR}/tick"
  log "⏳ ${1}"
}

# --- registry -------------------------------------------------------------------------------------------------

function manifest() {
  registry "${1}" "manifests/${2}"
}

function referrers() {
  registry "${1}" "referrers/${2}" 2> /dev/null || true
}

# registry NAME PATH: a GET from the image NAME on ghcr.io; the token goes in curl's config, not its arguments.
function registry() {
  printf 'header = "Authorization: Bearer %s"\nheader = "Accept: %s"\n' "$(registry_token "${1}")" \
    "${MANIFEST_TYPES}" | curl -fsSL --max-time 30 -K - "https://ghcr.io/v2/$(ctx owner | lower)/${1}/${2}"
}

# registry_token NAME: a pull token for the image NAME, fetched once.
function registry_token() {
  local -r file="${CLEAN_DIR}/registry-token-${1//\//_}"
  if [ ! -s "${file}" ]; then
    printf 'user = "%s:%s"\n' "$(ctx login)" "$(token)" | curl -fsSL --max-time 30 -K - \
      "https://ghcr.io/token?service=ghcr.io&scope=repository:$(ctx owner | lower)/${1}:pull" \
      | jq -r .token > "${file}"
  fi
  cat "${file}"
}

# --- verification ---------------------------------------------------------------------------------------------

# verify: a second inventory finds nothing stale, the kept release and images unchanged, the deleted
# attestations gone and the tags as they were.
function verify() {
  local failed=0
  log '🔎 Verifying'
  mv "${INV}" "${CLEAN_DIR}/before"
  mkdir -p "${INV}"
  inventory
  verify_lists || failed=1
  verify_attestations || failed=1
  verify_tags || failed=1
  report_deleted
  [ "${failed}" -eq 0 ] || { log "❌ The clean-up is incomplete: see ${LOG_FILE}"; exit 1; }
  log "✅ verified: nothing stale is published from $(ctx repo), and its tags are unchanged"
}

function verify_lists() {
  local category failed=0
  for category in ${CATEGORIES}; do
    [ "$(lines "${category}.delete")" -eq 0 ] && continue
    log "❌ ${category}: $(lines "${category}.delete") still published (${INV}/${category}.delete)"
    failed=1
  done
  diff "${CLEAN_DIR}/before/releases.keep" "${INV}/releases.keep" > /dev/null || { log '❌ The kept release changed'; failed=1; }
  diff "${CLEAN_DIR}/before/packages.keep" "${INV}/packages.keep" > /dev/null || { log '❌ The kept package versions changed'; failed=1; }
  return "${failed}"
}

function verify_attestations() {
  local digest failed=0
  for digest in $(jq -r . "${CLEAN_DIR}/before/attestations.all"); do
    [ "$(attestation_count "${digest}")" -eq 0 ] && continue
    log "❌ The attestations of ${digest} are still published"
    failed=1
  done
  return "${failed}"
}

function verify_tags() {
  git ls-remote --tags origin | diff "${CLEAN_DIR}/tags.before" - > /dev/null && return 0
  log '❌ The tags on origin changed during the clean-up'
  return 1
}

function report_deleted() {
  local category failed
  for category in ${CATEGORIES}; do
    failed=0
    [ ! -f "${CLEAN_DIR}/before/${category}.failed" ] || failed="$(wc -l < "${CLEAN_DIR}/before/${category}.failed" | tr -d ' ')"
    log "🗑️  ${category}: $(( $(wc -l < "${CLEAN_DIR}/before/${category}.delete") - failed )) deleted, ${failed} failed"
  done
}

main "${@:-}"
