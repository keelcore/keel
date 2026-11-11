#!/usr/bin/env bash
# scripts/lib/dev-image.sh
# Naming and identity of a repository's tool images (containers/NAME/Containerfile, and each build
# file config/tool-images.local.txt lists: tool_images), shared by scripts/dev/run.sh (runs make
# targets in them) and scripts/dev/publish.sh (builds and pushes them to the registry).
#
# An image is identified by its build file, wherever it lives: the build file, every file it
# COPYs from its context directory, and — for a build file with `ARG BASE_DEV_IMAGE` — the .standards
# dev image it builds on. Published images are tagged ctx-<hash>, and containers/NAME/image.ref (a
# listed image: <BUILD FILE>.image.ref) names the one built from each commit
# (scripts/dev/update-dev-image-ref.sh):
#   ghcr.io/keelcore/<repo>-<name>:ctx-<hash>
#
# Environment: KEELCORE_DEV_IN_HOOK (default empty; scripts/hooks/pre-commit sets 1: run.sh never builds
# in a commit), KEELCORE_BUILD_CPUS (default 4) and KEELCORE_BUILD_MEMORY (default 8g), the image
# builder's size, KEELCORE_DEV_BUILDER_TIMEOUT (default 300) seconds per builder step,
# KEELCORE_DEV_REGISTRY (default ghcr.io/keelcore), KEELCORE_DEV_REPO (default empty; run.sh passes the
# repository name into a tool image), KEELCORE_DEV_PULL_TIMEOUT (default 1800) seconds per pull or build,
# KEELCORE_DEV_PROGRESS_SECONDS (default 30) between progress lines.

# shellcheck source=progress.sh
source "$(dirname "${BASH_SOURCE[0]}")/progress.sh"

# shellcheck source=container-system.sh
source "$(dirname "${BASH_SOURCE[0]}")/container-system.sh"

declare -rx KEELCORE_DEV_IN_HOOK="${KEELCORE_DEV_IN_HOOK:-}"
declare -rx KEELCORE_BUILD_CPUS="${KEELCORE_BUILD_CPUS:-4}"
declare -rx KEELCORE_BUILD_MEMORY="${KEELCORE_BUILD_MEMORY:-8g}"
declare -rx KEELCORE_DEV_BUILDER_TIMEOUT="${KEELCORE_DEV_BUILDER_TIMEOUT:-300}"
declare -rx KEELCORE_DEV_REGISTRY="${KEELCORE_DEV_REGISTRY:-ghcr.io/keelcore}"
declare -rx KEELCORE_DEV_REPO="${KEELCORE_DEV_REPO:-}"
declare -rx KEELCORE_DEV_PULL_TIMEOUT="${KEELCORE_DEV_PULL_TIMEOUT:-1800}"
declare -rx KEELCORE_DEV_PROGRESS_SECONDS="${KEELCORE_DEV_PROGRESS_SECONDS:-30}"

# Inside a git hook, git exports the hook's repository layout (GIT_INDEX_FILE=.git/index, and
# GIT_DIR / GIT_WORK_TREE in some cases) as paths relative to that repository. These scripts run
# git in other repositories too (the .standards submodule, snapshots under /tmp), where those
# paths are wrong: drop them, so every git call reads the repository it is pointed at. The
# context hash reads tracked and untracked files from the working tree, so it is the same either
# way. A caller that imports GIT_INDEX_FILE (update-dev-image-ref.sh: the commit's own index, read
# only in this repository) keeps it: an import is read-only.
[[ "$(declare -p GIT_INDEX_FILE 2> /dev/null)" == 'declare -rx '* ]] || unset GIT_INDEX_FILE
unset GIT_DIR GIT_WORK_TREE GIT_COMMON_DIR GIT_PREFIX GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES

# dev_builder_args: Apple `container build` sizing for the builder it starts (2 CPUs / 2 GB by default,
# too small for the tool images): KEELCORE_BUILD_CPUS (4) and KEELCORE_BUILD_MEMORY (8g).
function dev_builder_args() {
  printf '%s\n' --cpus "${KEELCORE_BUILD_CPUS}" --memory "${KEELCORE_BUILD_MEMORY}"
}

# dev_ensure_builder: an Apple `container` builder already running at another size keeps that size
# (the flags only size a builder `container build` starts): restart it at dev_builder_args' size.
function dev_ensure_builder() {
  local -r cpus="${KEELCORE_BUILD_CPUS}" mem="${KEELCORE_BUILD_MEMORY}"
  local want_mb have
  case "${mem}" in
    *[gG]) want_mb=$(( ${mem%[gG]} * 1024 )) ;;
    *[mM]) want_mb="${mem%[mM]}" ;;
    *) want_mb="${mem}" ;;
  esac
  local -r limit="${KEELCORE_DEV_BUILDER_TIMEOUT}" status="$(mktemp)"
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
  printf '%s' "${KEELCORE_DEV_REGISTRY}"
}

# dev_repo_name DIR: DIR's repository name, as an image-name component — the same for every
# checkout of it (worktrees and submodules included): the name of its shared git directory
# (<repo>/.git, or <superproject>/.git/modules/<name> for a submodule).
function dev_repo_name() {
  # Inside a tool image the repository is mounted at /work: scripts/dev/run.sh passes its name.
  if [ -n "${KEELCORE_DEV_REPO}" ] && [ "$(cd "${1}" && pwd -P)" = "$(git rev-parse --show-toplevel)" ]; then
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

# tool_images [FROM]: every tool image of the repository at the working directory, one per line:
#   NAME<TAB>BUILD FILE<TAB>CONTEXT DIR<TAB>REF FILE
# first each containers/NAME/Containerfile (context ., named in containers/NAME/image.ref), then each
# line of config/tool-images.local.txt, `NAME<TAB>BUILD FILE<TAB>CONTEXT DIR` (named in
# <BUILD FILE>.image.ref, beside it). FROM: empty for the working tree, staged for the index. A
# malformed line, a name that is not [a-z0-9-] or that another image has, fails naming the line:
# capture the list (list="$(tool_images)") so the failure stops the caller.
function tool_images() {
  local -r from="${1:-}" list='config/tool-images.local.txt'
  local cf containers=''
  if [ "${from}" = 'staged' ]; then
    containers="$(git ls-files -c -- 'containers/*/Containerfile' | grep -E '^containers/[^/]+/Containerfile$' || true)"
  else
    for cf in containers/*/Containerfile; do
      [ -f "${cf}" ] && containers+="${cf}"$'\n'
    done
  fi
  {
    printf '%s\n' "${containers}" | awk 'NF { split($0, p, "/"); print "C\t" p[2] "\t" $0 }'
    tool_images_read "${list}" "${from}" | awk '{ print "L\t" NR "\t" $0 }'
  } | awk -F'\t' -v list="${list}" '
    $1 == "C" { name[$2] = 1; printf "%s\t%s\t.\tcontainers/%s/image.ref\n", $2, $3, $2; next }
    {
      line = $0; sub(/^L\t[0-9]+\t/, "", line); sub(/#.*/, "", line); sub(/[[:space:]]+$/, "", line)
      if (line == "") next
      n = split(line, f, "\t")
      where = list ":" $2
      if (n != 3 || f[2] == "" || f[3] == "") { bad = where ": expected NAME<TAB>BUILD FILE<TAB>CONTEXT DIR"; exit 1 }
      if (f[1] !~ /^[a-z0-9][a-z0-9-]*$/) { bad = where ": " f[1] " is not a name of [a-z0-9-]"; exit 1 }
      if (f[1] in name) { bad = where ": " f[1] " names another tool image"; exit 1 }
      name[f[1]] = 1
      ctx = f[3]; sub(/\/+$/, "", ctx); if (ctx == "") ctx = "."
      printf "%s\t%s\t%s\t%s.image.ref\n", f[1], f[2], ctx, f[2]
    }
    END { if (bad != "") { print "❌ " bad > "/dev/stderr"; exit 1 } }'
}

# tool_images_read FILE [FROM]: FILE as the working tree (or, with staged, the index) holds it; nothing when absent.
function tool_images_read() {
  if [ "${2:-}" = 'staged' ]; then
    git show ":${1}" 2> /dev/null || true
  elif [ -f "${1}" ]; then
    cat "${1}"
  fi
}

# dev_image_field NAME FIELD [DIR [FROM]]: one field of NAME's tool_images line in DIR (2: build file, 3:
# context dir, 4: ref file); an image not listed is containers/NAME.
function dev_image_field() {
  local -r name="${1}" field="${2}" dir="${3:-.}" from="${4:-}"
  local list
  list="$(cd "${dir}" && tool_images "${from}")" || return 1
  awk -F'\t' -v n="${name}" -v f="${field}" '$1 == n { print $f; found = 1; exit } END { exit !found }' <<< "${list}" \
    && return 0
  case "${field}" in
    2) printf 'containers/%s/Containerfile\n' "${name}" ;;
    3) printf '.\n' ;;
    4) printf 'containers/%s/image.ref\n' "${name}" ;;
  esac
}

# dev_containerfile NAME [DIR [FROM]]: the build file of DIR's NAME image.
function dev_containerfile() {
  dev_image_field "${1}" 2 "${2:-.}" "${3:-}"
}

# dev_context_dir NAME [DIR [FROM]]: the build context directory of DIR's NAME image (. for containers/NAME).
function dev_context_dir() {
  dev_image_field "${1}" 3 "${2:-.}" "${3:-}"
}

# dev_ref_file NAME [DIR [FROM]]: the file naming DIR's NAME image: containers/NAME/image.ref, or
# <BUILD FILE>.image.ref for a listed image.
function dev_ref_file() {
  dev_image_field "${1}" 4 "${2:-.}" "${3:-}"
}

# dev_has_base DIR NAME: whether DIR's NAME build file builds on the .standards dev image.
function dev_has_base() {
  grep -qE '^ARG BASE_DEV_IMAGE' "${1}/$(dev_containerfile "${2}" "${1}")"
}

# dev_context_files DIR NAME [staged]: the build file and every file it COPYs from its build context
# directory: the files the index holds with staged, else the tree's.
function dev_context_files() {
  local -r dir="${1}" from="${3:-}"
  local cf ctx
  cf="$(dev_containerfile "${2}" "${dir}" "${from}")"
  ctx="$(dev_context_dir "${2}" "${dir}" "${from}")"
  printf '%s\n' "${cf}"
  dev_context_read "${dir}" "${cf}" "${from}" | awk '
    { line = line $0 }
    /\\$/ { sub(/\\$/, " ", line); next }
    { print line; line = "" }
  ' | awk '$1 == "COPY" && $2 !~ /^--from=/ { for (i = 2; i < NF; i++) print $i }' \
    | while IFS= read -r src; do
        src="${src%/}"
        [ "${ctx}" = '.' ] || src="${ctx}/${src}"
        if [ "${from}" = 'staged' ]; then
          (cd "${dir}" && git ls-files -c -- "${src}")
        else
          (cd "${dir}" && git ls-files -co --exclude-standard -- "${src}")
        fi
      done | sort -u
}

# dev_context_read DIR FILE [staged]: FILE as the context sees it: the index's with staged, else the tree's.
function dev_context_read() {
  if [ "${3:-}" = 'staged' ]; then
    git -C "${1}" show ":${2}"
  else
    cat "${1}/${2}"
  fi
}

# dev_context_blob DIR FILE [staged]: FILE's blob hash, from the index or the tree as dev_context_read.
function dev_context_blob() {
  if [ "${3:-}" = 'staged' ]; then
    git -C "${1}" rev-parse ":${2}"
  else
    (cd "${1}" && git hash-object -- "${2}")
  fi
}

# dev_context_hash DIR NAME [BASE [staged]]: one hash over DIR's NAME build context (and BASE, the
# reference of the image it builds on).
function dev_context_hash() {
  local -r dir="${1}" name="${2}" base="${3:-}" from="${4:-}"
  {
    dev_context_files "${dir}" "${name}" "${from}" | while IFS= read -r f; do
      printf '%s %s\n' "$(dev_context_blob "${dir}" "${f}" "${from}")" "${f}"
    done
    [ -z "${base}" ] || printf 'base %s\n' "${base}"
  } | git hash-object --stdin | cut -c1-16
}

# dev_base_ref DIR NAME: the image NAME builds on: the name its .standards pins, empty if it builds on none.
function dev_base_ref() {
  dev_has_base "${1}" "${2}" || return 0
  head -1 "${1}/.standards/$(dev_ref_file dev "${1}/.standards")"
}

# dev_tree_hash DIR NAME [staged]: the context hash of DIR's NAME image, with the base it builds on.
function dev_tree_hash() {
  dev_context_hash "${1}" "${2}" "$(dev_base_ref "${1}" "${2}")" "${3:-}"
}

# dev_pin_ref DIR NAME [staged]: the name of the image built from NAME's context: REPOSITORY:ctx-HASH, the
# context as the index holds it with staged (the commit being made), else as the tree holds it.
function dev_pin_ref() {
  printf '%s:ctx-%s' "$(dev_repository "${1}" "${2}")" "$(dev_tree_hash "${1}" "${2}" "${3:-}")"
}

# dev_tool_image NAME: the reference of this repository's NAME tool image, on this machine: the published
# image, pulled, else a local build of the same context (scripts/dev/run.sh), so a script pulls an image
# instead of building one of its own.
function dev_tool_image() {
  local -r run="$(dirname "${BASH_SOURCE[0]}")/../dev/run.sh"
  bash "${run}" --image "${1}" --fetch-image >&2 || return 1
  bash "${run}" --image "${1}" --image-ref
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

# pull_image REF: fetch REF. Apple container's default progress prints nothing without a terminal, so
# it asks for plain lines; docker prints them either way.
function pull_image() {
  local -a progress=()
  [ "$(runtime)" != 'container' ] || progress=(--progress plain)
  dev_watch "pulling ${1}" "${KEELCORE_DEV_PULL_TIMEOUT}" \
    "$(runtime)" image pull ${progress[@]+"${progress[@]}"} "${1}"
}

# dev_watch LABEL LIMIT COMMAND...: run COMMAND, its output on stderr, with a progress line every
# KEELCORE_DEV_PROGRESS_SECONDS (default 30): elapsed time, the latest build step and line. Past LIMIT
# seconds it is stopped and fails, so no build or pull can hang. With DEV_WATCH_OUT set, the output goes
# to that file instead of stderr.
function dev_watch() {
  local -r label="${1}" limit="${2}"
  shift 2
  local -r every="${KEELCORE_DEV_PROGRESS_SECONDS}" out="$(mktemp)"
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
