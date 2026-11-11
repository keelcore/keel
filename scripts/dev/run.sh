#!/usr/bin/env bash
# scripts/dev/run.sh
# Run a command in one of the repository's tool images with the repository
# mounted read-only. Every make target that runs a tool goes through here, locally and in
# CI, so the tool versions are the ones baked into the image.
#
# Usage: run.sh [--image NAME] [--writable] COMMAND [ARG...]   (from the repository root)
#        run.sh [--image NAME] --image-ref     print the image reference; nothing fetched or built
#        run.sh [--image NAME] --fetch-image   put the image on this machine
#
# Images: containers/NAME/Containerfile (default NAME: dev), and each image config/tool-images.local.txt
# lists, built from its build file in its context directory (scripts/lib/dev-image.sh tool_images).
#
# Mounts: the repository read-only, except its output directories (builds, coverage,
# reports), which are writable: those listed in containers/writable, one per line, else
# target/. --writable mounts the whole repository writable, for the targets whose job is
# editing tracked files (format, coverage baseline). A submodule or worktree (its git
# directory lives outside it) is mounted as a standalone snapshot under /tmp: a clone holding
# its current files; a writable run's edits are copied back.
#
# Image: <repository>:ctx-<context hash>, on this machine or published;
# otherwise keelcore-NAME-<repo>:ctx-<context hash>, built locally from
# containers/NAME/Containerfile on first use. A Containerfile with
# `ARG BASE_DEV_IMAGE` gets the .standards dev image as that argument.
# Runtime: Apple `container` on macOS, docker on Linux.
# Inside the image (KEELCORE_DEV_IMAGE set) the command runs directly.
# Environment passed into the image: each name the script COMMAND runs imports (governance/bash.md R37),
# with the files it sources, its project hook and the scripts it runs by path (env_import_chain), when set;
# every GITHUB_*, RUNNER_* and ACTIONS_* variable and CI (the Actions context: npm and cosign build provenance
# from it; the OIDC token request); and TZ. By name, in a private --env-file removed after the run: no value
# from this environment reaches the runtime's arguments or the file, which holds only run.sh's own
# KEELCORE_DEV_REPO and DOCKER_CONFIG (the mounted login) as values. A COMMAND that runs no script gets the
# Actions context and TZ.
# Environment: DOCKER_CONFIG (default empty).
# Environment: KEELCORE_DEV_BUILD_TIMEOUT (default 3600).
# Environment: KEELCORE_DEV_MEMORY (default 4g).
# Project hook: run.local.sh runs first, when present, with this script's arguments
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

# shellcheck source=../lib/dev-image.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/dev-image.sh"

# shellcheck source=../lib/env-imports.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/env-imports.sh"

declare -rx KEELCORE_DEV_MEMORY="${KEELCORE_DEV_MEMORY:-4g}"
declare -rx KEELCORE_DEV_BUILD_TIMEOUT="${KEELCORE_DEV_BUILD_TIMEOUT:-3600}"
declare -rx DOCKER_CONFIG="${DOCKER_CONFIG:-}"

# Set by run_in, removed on exit: the snapshot a submodule or worktree runs in, and the env file.
SNAPSHOT=''
ENV_FILE=''

function main() {
  local mode='ro' name='dev'
  while [ "${#}" -gt 0 ]; do
    case "${1}" in
      --writable) mode='rw'; shift ;;
      --image) name="${2:-}"; shift 2 ;;
      --image-ref)
        IMAGE_REF_ONLY=1
        dev_image . "${name}"
        printf '\n'
        return ;;
      --fetch-image)
        fetch_image "${name}"
        return ;;
      *) break ;;
    esac
  done
  validate_args "$@"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  if [ -n "${KEELCORE_DEV_IMAGE:-}" ]; then
    exec "$@"
  fi
  local image
  if ! image="$(dev_image . "${name}")"; then
    # A commit never stops for a missing image, so a new image can bootstrap; CI runs the check.
    [ -z "${KEELCORE_DEV_IN_HOOK:-}" ] || { log "⚠️  skipped in this commit (CI runs it): $*"; exit 0; }
    exit 1
  fi
  run_in "${image}" "${mode}" "$@"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keelcore_dev_run.log' >&2
}

function validate_args() {
  if [ "${#}" -eq 0 ] || [ -z "${1:-}" ]; then
    log '❌ Usage: scripts/dev/run.sh COMMAND [ARG...]'
    exit 1
  fi
}

# dev_image DIR NAME: DIR's NAME image for its context: the pinned name or a local build already on this
# machine, else the published one, else a local build made now. Inside a git hook: hook_image.
function dev_image() {
  local -r dir="${1}" name="${2}"
  if [ -n "${KEELCORE_DEV_IN_HOOK:-}" ]; then
    hook_image "${dir}" "${name}"
    return
  fi
  local -r cf="$(dev_containerfile "${name}" "${dir}")"
  [ -f "${dir}/${cf}" ] || { log "❌ ${dir}/${cf} not found"; exit 1; }
  local base hash tag
  base="$(dev_base_ref "${dir}" "${name}")"
  hash="$(dev_tree_hash "${dir}" "${name}")"
  tag="keelcore-${name}-$(dev_repo_name "${dir}"):ctx-${hash}"
  # On this machine first (the pinned name, then a local build of the context), so the registry is asked
  # only when neither is here.
  if "$(runtime)" image inspect "$(dev_pin_ref "${dir}" "${name}")" > /dev/null 2>&1; then
    dev_pin_ref "${dir}" "${name}"
    return 0
  fi
  if "$(runtime)" image inspect "${tag}" > /dev/null 2>&1; then
    printf '%s' "${tag}"
    return 0
  fi
  if dev_published_ref "$(dev_repository "${dir}" "${name}")" "${hash}" > /dev/null; then
    dev_pin_ref "${dir}" "${name}"
    return 0
  fi
  if [ -z "${IMAGE_REF_ONLY:-}" ]; then
    log "ℹ️  ctx-${hash} of ${dir} is not published yet; building it locally"
    local build_base=''
    [ -z "${base}" ] || build_base="$(dev_image "${dir}/.standards" dev)" || return 1
    build_image "${dir}" "${name}" "${tag}" "${build_base}" || { log "❌ building ${tag} failed"; exit 1; }
  fi
  printf '%s' "${tag}"
}

# hook_image DIR NAME: the image holding this tree's tools, when it is on this machine: the one its
# context names, else a local build of that context. A commit never fetches or builds, so without
# either it says so and returns 1: the checks are skipped and CI runs them.
function hook_image() {
  local -r dir="${1}" name="${2}"
  local hash ref
  hash="$(dev_tree_hash "${dir}" "${name}")"
  for ref in "$(dev_pin_ref "${dir}" "${name}")" "keelcore-${name}-$(dev_repo_name "${dir}"):ctx-${hash}"; do
    if "$(runtime)" image inspect "${ref}" > /dev/null 2>&1; then
      log "ℹ️  hook uses ${ref}"
      printf '%s' "${ref}"
      return 0
    fi
  done
  log "⚠️  no image for this tree (ctx-${hash}) on this machine; to check commits here: make pull-dev-image"
  return 1
}

# build_image DIR NAME TAG BASE: build DIR's NAME image from its build file, in its context directory, as TAG.
function build_image() {
  local -r dir="${1}" name="${2}" tag="${3}" base="${4}"
  local cf ctx
  cf="$(dev_containerfile "${name}" "${dir}")"
  ctx="$(dev_context_dir "${name}" "${dir}")"
  if [ "${ctx}" = '.' ]; then ctx="${dir}"; else ctx="${dir}/${ctx}"; fi
  local -a args=(build -f "${dir}/${cf}" -t "${tag}")
  [ -z "${base}" ] || args+=(--build-arg "BASE_DEV_IMAGE=${base}")
  if [ "$(runtime)" = 'container' ]; then
    local opt
    dev_ensure_builder
    while IFS= read -r opt; do args+=("${opt}"); done < <(dev_builder_args)
  fi
  log "🔨 Building image ${tag}..."
  dev_watch "building ${tag}" "${KEELCORE_DEV_BUILD_TIMEOUT}" "$(runtime)" "${args[@]}" "${ctx}"
}

# fetch_image NAME: NAME's image on this machine, built or pulled, ready for runs that follow.
function fetch_image() {
  local image
  image="$(dev_image . "${1}")" || exit 1
  "$(runtime)" image inspect "${image}" > /dev/null 2>&1 || pull_image "${image}" >&2
}

# output_dirs ROOT: the writable output directories (repository-relative): the lines of
# containers/writable (comments and blanks skipped), else target.
function output_dirs() {
  local -r file="${1}/containers/writable"
  if [ -f "${file}" ]; then
    grep -vE '^[[:space:]]*(#|$)' "${file}"
  else
    printf 'target\n'
  fi
}

# snapshot DIR: a standalone copy of DIR (a submodule or worktree) under /tmp, this run's own — a clone (its
# own .git, so git works without the outer repository) holding DIR's current files, tracked
# and untracked; each submodule of DIR is a standalone copy of its own (snapshot_into).
function snapshot() {
  local -r dir="${1}"
  mkdir -p '/tmp/keelcore-dev-snapshot'
  local -r dest="$(mktemp -d "/tmp/keelcore-dev-snapshot/$(dev_repo_name "${dir}")-XXXXXXXX")"
  snapshot_into "${dir}" "${dest}"
  printf '%s' "${dest}"
}

# snapshot_into DIR DEST: make DEST a clone of DIR holding DIR's current files. A submodule's
# own .git is a pointer into its superproject's .git, which the container never sees, so
# submodules are not copied as files: each becomes a snapshot of its own, recursively.
function snapshot_into() {
  local -r dir="${1}" dest="${2}"
  rm -rf "${dest}"
  git clone --quiet --no-hardlinks "${dir}" "${dest}"
  local -r subs="$(git -C "${dir}" ls-files -s | awk '$1 == "160000" { print $4 }')"
  # Every file of the clone's commit that DIR no longer has (deleted, staged or not) goes.
  git -C "${dest}" ls-files -z | while IFS= read -r -d '' f; do
    [ -e "${dir}/${f}" ] || printf '%s\0' "${f}"
  done | (cd "${dest}" && xargs -0 rm -f --)
  (cd "${dir}" && git ls-files -co --exclude-standard -z | while IFS= read -r -d '' f; do
      [ -e "${f}" ] && ! grep -qxF -- "${f}" <<< "${subs}" && printf '%s\0' "${f}"
    done | tar --null -T - -cf -) | tar -xf - -C "${dest}"
  local sub
  while IFS= read -r sub; do
    [ -n "${sub}" ] && [ -e "${dir}/${sub}/.git" ] || continue
    rm -rf "${dest:?}/${sub}"
    snapshot_into "${dir}/${sub}" "${dest}/${sub}"
  done <<< "${subs}"
  # The index lists DEST's files (tools that read `git ls-files` see what is there).
  git -C "${dest}" add -A
}

# sync_back SNAPSHOT DIR: copy back to DIR what a writable run's command changed, measured against
# the snapshot's index, which holds what DIR held when the run started. A file edited in DIR since
# then keeps the edit. Files are replaced, never written into: one may be a script running now.
function sync_back() {
  local -r snap="${1}" dir="${2}"
  local f base now
  (cd "${snap}" && { git diff --name-only -z; git ls-files -o --exclude-standard -z; }) | while IFS= read -r -d '' f; do
    # A submodule entry is a directory, not a file: its own snapshot holds its files.
    [ -f "${snap}/${f}" ] || continue
    cmp -s "${snap}/${f}" "${dir}/${f}" && continue
    if [ -e "${dir}/${f}" ]; then
      base="$(git -C "${snap}" rev-parse --verify --quiet ":${f}" || true)"
      now="$(git -C "${snap}" hash-object --no-filters "${dir}/${f}")"
      if [ "${now}" != "${base}" ]; then
        log "⚠️  ${f}: kept the version in the tree, edited since this run started"
        continue
      fi
    fi
    mkdir -p "$(dirname "${dir}/${f}")"
    cp -p "${snap}/${f}" "${dir}/${f}.sync-back.$$"
    mv -f "${dir}/${f}.sync-back.$$" "${dir}/${f}"
    log "✍️  ${f}"
  done
}

# run_in IMAGE MODE COMMAND...: COMMAND in IMAGE with this repository mounted at /work (MODE ro
# or rw) and its output directories writable, working directory the same relative path as here.
# A submodule (its git directory lives in the superproject) is mounted as a standalone snapshot
# (/tmp); a writable run's edits are copied back.
function remove_run_files() {
  [ -z "${SNAPSHOT}" ] || rm -rf "${SNAPSHOT}" || true
  [ -z "${ENV_FILE}" ] || rm -f "${ENV_FILE}" || true
}

# command_script COMMAND...: the script COMMAND runs: PATH.sh, or bash [OPTION...] PATH.sh; nothing for any
# other command, bash -c included.
function command_script() {
  if [ "${1:-}" = 'bash' ]; then
    shift
    while [[ "${1:-}" == -* ]]; do
      [ "${1}" != '-c' ] || return 0
      shift
    done
  fi
  [[ "${1:-}" != *.sh ]] || printf '%s' "${1}"
}

# passed_names SCRIPT: the names passed into the image, each set here: SCRIPT's imports (env_import_chain; none
# without a script), the Actions context and TZ. DOCKER_CONFIG and KEELCORE_DEV_REPO are run.sh's own values.
function passed_names() {
  local chain='' name
  [ -z "${1}" ] || chain="$(env_import_chain "${1}")" || return 1
  { printf '%s\n' "${chain}" CI TZ; compgen -e | grep -E '^(GITHUB|RUNNER|ACTIONS)_' || true; } | sort -u \
    | while IFS= read -r name; do
      case "${name}" in '' | DOCKER_CONFIG | KEELCORE_DEV_REPO) continue ;; esac
      [ -z "${!name+set}" ] || printf '%s\n' "${name}"
    done
}

function run_in() {
  local -r image="${1}" mode="${2}"
  shift 2
  local here src name
  here="$(git rev-parse --show-toplevel)"
  src="${here}"
  trap remove_run_files EXIT
  # The environment, by name, in a private file (mktemp: mode 600): the runtime copies each value from this
  # process, so none (tokens included) reaches its arguments or the file.
  ENV_FILE="$(mktemp "${TMPDIR:-/tmp}/keelcore-run-env.XXXXXX")"
  passed_names "$(command_script "$@")" > "${ENV_FILE}"
  printf 'KEELCORE_DEV_REPO=%s\n' "$(dev_repo_name "${here}")" >> "${ENV_FILE}"
  # A submodule or worktree: its .git is a file pointing at a git directory outside it.
  if [ -f "${here}/.git" ]; then
    src="$(snapshot "${here}")"
    SNAPSHOT="${src}"
  fi
  # git reports the physical path (macOS /tmp and /var are symlinks into /private).
  local rel
  rel="$(pwd -P)"
  rel="${rel#"${here}"}"
  local -a args=(run --rm -i -v "${src}:/work:${mode}" -w "/work${rel}" --env-file "${ENV_FILE}")
  # The job's registry login (DOCKER_CONFIG, one login per job), read-only: tools in the image reuse
  # it instead of logging in themselves.
  if [ -n "${DOCKER_CONFIG}" ] && [ -d "${DOCKER_CONFIG}" ]; then
    args+=(-v "${DOCKER_CONFIG}:/run/keelcore-docker-config:ro")
    printf 'DOCKER_CONFIG=/run/keelcore-docker-config\n' >> "${ENV_FILE}"
  fi
  # Apple container caps a container at 1 GB unless told otherwise (docker does not cap).
  [ "$(runtime)" = 'container' ] && args+=(-m "${KEELCORE_DEV_MEMORY}")
  local out
  if [ "${mode}" = 'ro' ]; then
    while IFS= read -r out; do
      mkdir -p "${here}/${out}" "${src}/${out}"
      args+=(-v "${here}/${out}:/work/${out}")
    done < <(output_dirs "${here}")
  fi
  # A TTY only when both ends are terminals: git runs hooks with stdin from /dev/null, and a TTY
  # on a non-terminal stdin fails (Apple container: "Operation not supported by device").
  [ -t 0 ] && [ -t 1 ] && args+=(-t)
  # GitHub Actions file commands: the event payload (read) and the output / summary files (append).
  local file fmode mounted=" "
  for name in GITHUB_EVENT_PATH GITHUB_OUTPUT GITHUB_STEP_SUMMARY; do
    file="${!name:-}"
    [ -n "${file}" ] && [ -e "${file}" ] || continue
    case "${mounted}" in *" $(dirname "${file}") "*) continue ;; esac
    mounted+="$(dirname "${file}") "
    fmode='rw'
    [ "${name}" = 'GITHUB_EVENT_PATH' ] && fmode='ro'
    args+=(-v "$(dirname "${file}"):$(dirname "${file}"):${fmode}")
  done
  # Not exec: the env file and the snapshot are removed when the command ends.
  local rc=0
  "$(runtime)" "${args[@]}" "${image}" "$@" || rc=$?
  [ "${src}" = "${here}" ] || [ "${mode}" != 'rw' ] || sync_back "${src}" "${here}"
  return "${rc}"
}

main "$@"
