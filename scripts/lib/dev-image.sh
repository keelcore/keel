#!/usr/bin/env bash
# scripts/lib/dev-image.sh
# Naming and identity of a repository's tool images (containers/NAME/Containerfile),
# shared by scripts/dev/run.sh (runs make targets in them) and
# scripts/dev/publish.sh (builds and pushes them to the registry).
#
# An image is identified by its build context: the Containerfile, every file it
# COPYs, and — for a Containerfile with `ARG BASE_DEV_IMAGE` — the .standards dev
# image it builds on. Published images are tagged ctx-<hash>, and
# containers/NAME/image.ref pins one by digest:
#   ghcr.io/keelcore/<repo>-<name>:ctx-<hash>@sha256:<digest>

# Inside a git hook, git exports the hook's repository layout (GIT_INDEX_FILE=.git/index, and
# GIT_DIR / GIT_WORK_TREE in some cases) as paths relative to that repository. These scripts run
# git in other repositories too (the .standards submodule, snapshots under /tmp), where those
# paths are wrong: drop them, so every git call reads the repository it is pointed at. The
# context hash reads tracked and untracked files from the working tree, so it is the same either
# way. KEELCORE_DEV_IN_HOOK records that this runs inside a hook (run.sh never builds there).
if [ -n "${GIT_INDEX_FILE:-}" ]; then
  export KEELCORE_DEV_IN_HOOK=1
fi
unset GIT_INDEX_FILE GIT_DIR GIT_WORK_TREE GIT_COMMON_DIR GIT_PREFIX GIT_OBJECT_DIRECTORY \
  GIT_ALTERNATE_OBJECT_DIRECTORIES

# dev_builder_args: Apple `container build` sizing for the builder it starts (2 CPUs / 2 GB by default,
# too small for the tool images): KEELCORE_BUILD_CPUS (4) and KEELCORE_BUILD_MEMORY (8g).
function dev_builder_args() {
  printf '%s\n' --cpus "${KEELCORE_BUILD_CPUS:-4}" --memory "${KEELCORE_BUILD_MEMORY:-8g}"
}

# dev_ensure_builder: an Apple `container` builder already running at another size keeps that size
# (the flags only size a builder `container build` starts): restart it at dev_builder_args' size.
function dev_ensure_builder() {
  local -r cpus="${KEELCORE_BUILD_CPUS:-4}" mem="${KEELCORE_BUILD_MEMORY:-8g}"
  local want_mb have
  case "${mem}" in
    *[gG]) want_mb=$(( ${mem%[gG]} * 1024 )) ;;
    *[mM]) want_mb="${mem%[mM]}" ;;
    *) want_mb="${mem}" ;;
  esac
  have="$(container builder status 2> /dev/null | awk 'NR == 2 && $3 == "running" { print $5 " " $6 }')"
  [ -n "${have}" ] || return 0
  [ "${have}" = "${cpus} ${want_mb}" ] && return 0
  container builder stop > /dev/null 2>&1 || true
  container builder start --cpus "${cpus}" --memory "${mem}" > /dev/null
}

# dev_registry: where images are published (KEELCORE_DEV_REGISTRY overrides).
function dev_registry() {
  printf '%s' "${KEELCORE_DEV_REGISTRY:-ghcr.io/keelcore}"
}

# dev_repo_name DIR: DIR's repository name, as an image-name component — the same for every
# checkout of it (worktrees and submodules included): the name of its shared git directory
# (<repo>/.git, or <superproject>/.git/modules/<name> for a submodule).
function dev_repo_name() {
  # Inside a tool image the repository is mounted at /work: scripts/dev/run.sh passes its name.
  if [ -n "${KEELCORE_DEV_REPO:-}" ] && [ "$(cd "${1}" && pwd -P)" = "$(git rev-parse --show-toplevel)" ]; then
    printf '%s\n' "${KEELCORE_DEV_REPO}"
    return 0
  fi
  local common
  common="$(cd "${1}" && git rev-parse --path-format=absolute --git-common-dir)"
  [ "$(basename "${common}")" = '.git' ] && common="$(dirname "${common}")"
  basename "${common}" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9\n-' '-' | sed -E 's/^-+//'
}

# dev_repository DIR NAME: the registry repository of DIR's NAME image.
function dev_repository() {
  printf '%s/%s-%s' "$(dev_registry)" "$(dev_repo_name "${1}")" "${2}"
}

function dev_containerfile() {
  printf 'containers/%s/Containerfile' "${1}"
}

function dev_ref_file() {
  printf 'containers/%s/image.ref' "${1}"
}

# dev_has_base DIR NAME: whether DIR's NAME Containerfile builds on the .standards dev image.
function dev_has_base() {
  grep -qE '^ARG BASE_DEV_IMAGE' "${1}/$(dev_containerfile "${2}")"
}

# dev_context_files DIR NAME: the Containerfile and every file it COPYs from the build context.
function dev_context_files() {
  local -r dir="${1}"
  local cf
  cf="$(dev_containerfile "${2}")"
  printf '%s\n' "${cf}"
  awk '
    { line = line $0 }
    /\\$/ { sub(/\\$/, " ", line); next }
    { print line; line = "" }
  ' "${dir}/${cf}" | awk '$1 == "COPY" && $2 !~ /^--from=/ { for (i = 2; i < NF; i++) print $i }' \
    | while IFS= read -r src; do
        (cd "${dir}" && git ls-files -co --exclude-standard -- "${src%/}")
      done | sort -u
}

# dev_context_hash DIR NAME [BASE]: one hash over DIR's NAME build context (and BASE, the
# reference of the image it builds on).
function dev_context_hash() {
  local -r dir="${1}" name="${2}" base="${3:-}"
  {
    dev_context_files "${dir}" "${name}" | while IFS= read -r f; do
      printf '%s %s\n' "$(cd "${dir}" && git hash-object -- "${f}")" "${f}"
    done
    [ -z "${base}" ] || printf 'base %s\n' "${base}"
  } | git hash-object --stdin | cut -c1-16
}

# dev_pinned_ref DIR NAME HASH: the image.ref reference when it was built from context HASH.
function dev_pinned_ref() {
  local -r file="${1}/$(dev_ref_file "${2}")" hash="${3}"
  [ -s "${file}" ] || return 1
  local ref
  read -r ref < "${file}"
  case "${ref}" in
    *":ctx-${hash}@sha256:"*) printf '%s' "${ref}" ;;
    *) return 1 ;;
  esac
}

# dev_published_ref REPOSITORY HASH: REPOSITORY:ctx-HASH@<digest> when the registry holds the
# multi-arch image for build context HASH (the dev-image workflow publishes it); fails when it
# does not, or the registry cannot be reached. Anonymous pull token, as for any public image.
# dev_resolve_ref DIR NAME HASH: the image of this build context: the pinned reference when image.ref
# pins it, else the published one. Fails when neither exists; what happens then is the caller's.
function dev_resolve_ref() {
  local -r dir="${1}" name="${2}" hash="${3}"
  dev_pinned_ref "${dir}" "${name}" "${hash}" \
    || dev_published_ref "$(dev_repository "${dir}" "${name}")" "${hash}"
}

function dev_published_ref() {
  local -r repository="${1}" hash="${2}"
  local -r host="${repository%%/*}" path="${repository#*/}"
  local token digest
  token="$(curl -fsS --max-time 5 "https://${host}/token?scope=repository:${path}:pull" 2> /dev/null \
    | sed -nE 's/.*"token":"([^"]+)".*/\1/p')" || return 1
  [ -n "${token}" ] || return 1
  digest="$(curl -fsSI --max-time 5 -H "Authorization: Bearer ${token}" \
    -H 'Accept: application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json' \
    "https://${host}/v2/${path}/manifests/ctx-${hash}" 2> /dev/null \
    | tr -d '\r' | sed -nE 's/^[Dd]ocker-[Cc]ontent-[Dd]igest: (sha256:[0-9a-f]{64})$/\1/p')" || return 1
  [ -n "${digest}" ] || return 1
  printf '%s:ctx-%s@%s' "${repository}" "${hash}" "${digest}"
}
