#!/usr/bin/env bash
# scripts/lib/dev-image.sh
# Naming and identity of a repository's tool images (containers/NAME/Containerfile),
# shared by scripts/dev/run.sh (runs make targets in them) and
# scripts/dev/publish.sh (builds and pushes them to the registry).
#
# An image is identified by its build context: the Containerfile, every file it
# COPYs, and — for a Containerfile with `ARG BASE_DEV_IMAGE` — the .standards dev
# image it builds on. Published images are tagged ctx-<hash>, and containers/NAME/image.ref names the
# one built from each commit (scripts/dev/update-dev-image-ref.sh):
#   ghcr.io/keelcore/<repo>-<name>:ctx-<hash>

# shellcheck source=progress.sh
. "$(dirname "${BASH_SOURCE[0]}")/progress.sh"

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
  local -r limit="${KEELCORE_DEV_BUILDER_TIMEOUT:-300}" status="$(mktemp)"
  DEV_WATCH_OUT="${status}" dev_watch 'checking the image builder' "${limit}" container builder status \
    || { rm -f "${status}"; exit 1; }
  have="$(awk 'NR == 2 && $3 == "running" { print $5 " " $6 }' "${status}")"
  rm -f "${status}"
  [ -n "${have}" ] || return 0
  [ "${have}" = "${cpus} ${want_mb}" ] && return 0
  dev_watch 'stopping the image builder' "${limit}" container builder stop || true
  dev_watch 'starting the image builder' "${limit}" container builder start --cpus "${cpus}" --memory "${mem}" || exit 1
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
  dev_context_read "${dir}" "${cf}" | awk '
    { line = line $0 }
    /\\$/ { sub(/\\$/, " ", line); next }
    { print line; line = "" }
  ' | awk '$1 == "COPY" && $2 !~ /^--from=/ { for (i = 2; i < NF; i++) print $i }' \
    | while IFS= read -r src; do
        if [ -n "${DEV_CONTEXT_STAGED:-}" ]; then
          (cd "${dir}" && git ls-files -c -- "${src%/}")
        else
          (cd "${dir}" && git ls-files -co --exclude-standard -- "${src%/}")
        fi
      done | sort -u
}

# dev_context_read DIR FILE: FILE as the context sees it: staged with DEV_CONTEXT_STAGED, else the tree's.
function dev_context_read() {
  if [ -n "${DEV_CONTEXT_STAGED:-}" ]; then
    git -C "${1}" show ":${2}"
  else
    cat "${1}/${2}"
  fi
}

# dev_context_blob DIR FILE: FILE's blob hash, staged or in the tree as dev_context_read.
function dev_context_blob() {
  if [ -n "${DEV_CONTEXT_STAGED:-}" ]; then
    git -C "${1}" rev-parse ":${2}"
  else
    (cd "${1}" && git hash-object -- "${2}")
  fi
}

# dev_context_hash DIR NAME [BASE]: one hash over DIR's NAME build context (and BASE, the
# reference of the image it builds on).
function dev_context_hash() {
  local -r dir="${1}" name="${2}" base="${3:-}"
  {
    dev_context_files "${dir}" "${name}" | while IFS= read -r f; do
      printf '%s %s\n' "$(dev_context_blob "${dir}" "${f}")" "${f}"
    done
    [ -z "${base}" ] || printf 'base %s\n' "${base}"
  } | git hash-object --stdin | cut -c1-16
}

# dev_base_ref DIR NAME: the image NAME builds on: the name its .standards pins, empty if it builds on none.
function dev_base_ref() {
  dev_has_base "${1}" "${2}" || return 0
  head -1 "${1}/.standards/$(dev_ref_file dev)"
}

# dev_tree_hash DIR NAME: the context hash of DIR's NAME image, with the base it builds on.
function dev_tree_hash() {
  dev_context_hash "${1}" "${2}" "$(dev_base_ref "${1}" "${2}")"
}

# dev_pin_ref DIR NAME: the name of the image built from NAME's context: REPOSITORY:ctx-HASH.
function dev_pin_ref() {
  printf '%s:ctx-%s' "$(dev_repository "${1}" "${2}")" "$(dev_tree_hash "${1}" "${2}")"
}

# dev_published_ref REPOSITORY HASH: REPOSITORY:ctx-HASH when the registry holds it (registry metadata
# only); a context not published yet, or no registry, is not found.
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
  printf '%s:ctx-%s' "${repository}" "${hash}"
}

# runtime: the container runtime of this host.
function runtime() {
  if [ "$(uname -s)" = 'Darwin' ]; then printf 'container'; else printf 'docker'; fi
}

# pull_image REF: fetch REF. Apple container's default progress prints nothing without a terminal, so
# it asks for plain lines; docker prints them either way.
function pull_image() {
  local -a progress=()
  [ "$(runtime)" != 'container' ] || progress=(--progress plain)
  dev_watch "pulling ${1}" "${KEELCORE_DEV_PULL_TIMEOUT:-1800}" \
    "$(runtime)" image pull ${progress[@]+"${progress[@]}"} "${1}"
}

# dev_watch LABEL LIMIT COMMAND...: run COMMAND, its output on stderr, with a progress line every
# KEELCORE_DEV_PROGRESS_SECONDS (default 30): elapsed time, the latest build step and line. Past LIMIT
# seconds it is stopped and fails, so no build or pull can hang. With DEV_WATCH_OUT set, the output goes
# to that file instead of stderr.
function dev_watch() {
  local -r label="${1}" limit="${2}"
  shift 2
  local -r every="${KEELCORE_DEV_PROGRESS_SECONDS:-30}" out="$(mktemp)"
  "$@" > "${out}" 2>&1 &
  local -r pid="${!}" started="${SECONDS}"
  local shown=0 next=$(( SECONDS + every )) rc=0
  while kill -0 "${pid}" 2> /dev/null; do
    [ -n "${DEV_WATCH_OUT:-}" ] || shown="$(dev_watch_show "${out}" "${shown}")"
    if [ $(( SECONDS - started )) -ge "${limit}" ]; then
      pkill -P "${pid}" 2> /dev/null || true
      kill "${pid}" 2> /dev/null || true
      wait "${pid}" 2> /dev/null || true
      log "❌ ${label}: stopped at its time limit of $(elapsed_mmss "${limit}")"
      rm -f "${out}"
      return 124
    fi
    if [ "${SECONDS}" -ge "${next}" ]; then
      log "⏳ $(elapsed_mmss $(( SECONDS - started ))) ${label}: $(progress_latest "${out}")"
      next=$(( SECONDS + every ))
    fi
    sleep 1
  done
  wait "${pid}" || rc=$?
  if [ -n "${DEV_WATCH_OUT:-}" ]; then
    cp "${out}" "${DEV_WATCH_OUT}"
  else
    dev_watch_show "${out}" "${shown}" > /dev/null
  fi
  rm -f "${out}"
  return "${rc}"
}

# dev_watch_show OUT SHOWN: print OUT's lines after the first SHOWN to stderr; print the new count.
function dev_watch_show() {
  local -r total="$(wc -l < "${1}" | tr -d ' ')"
  [ "${total}" -le "${2}" ] || tail -n "+$(( ${2} + 1 ))" "${1}" | head -n "$(( total - ${2} ))" >&2
  printf '%s' "${total}"
}
