#!/usr/bin/env bash
# scripts/dev/run.sh
# Run a command in one of the repository's tool images with the repository
# mounted read-only. Every make target that runs a tool goes through here, locally and in
# CI, so the tool versions are the ones baked into the image.
#
# Usage: run.sh [--image NAME] [--writable] COMMAND [ARG...]   (from the repository root)
#
# Images: containers/NAME/Containerfile (default NAME: dev).
#
# Mounts: the repository read-only, except its output directories (builds, coverage,
# reports), which are writable: those listed in containers/writable, one per line, else
# target/. --writable mounts the whole repository writable, for the targets whose job is
# editing tracked files (format, coverage baseline). A submodule or worktree (its git
# directory lives outside it) is mounted as a standalone snapshot under /tmp: a clone holding
# its current files; a writable run's edits are copied back.
#
# Image: the digest-pinned reference in containers/NAME/image.ref when it was
# built from the current build context (the files the Containerfile COPYs);
# otherwise keelcore-NAME-<repo>:ctx-<context hash>, built locally from
# containers/NAME/Containerfile on first use. A Containerfile with
# `ARG BASE_DEV_IMAGE` gets the .standards dev image as that argument.
# Runtime: Apple `container` on macOS, docker on Linux.
# Inside the image (KEELCORE_DEV_IMAGE set) the command runs directly.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/dev-image.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/dev-image.sh"

# Environment passed into the container: every GITHUB_*, RUNNER_* and ACTIONS_* variable (the
# Actions context: npm and cosign build provenance from it; the OIDC token request), plus CI,
# PR metadata, coverage settings, and any names a project lists in KEELCORE_DEV_ENV.
readonly PASS_ENV="TZ CI PR_TITLE PR_BODY PR_AUTHOR BASE_SHA HEAD_SHA COVERAGE_DIR COVERAGE_THRESHOLD COVERAGE_BASE_FILE COVERAGE_PR_FILE
  GO_COVERAGE_IGNORE_REGEX CARGO_COVERAGE_FEATURES CARGO_COVERAGE_IGNORE_REGEX
  ${KEELCORE_DEV_ENV:-}"

function main() {
  local mode='ro' name='dev'
  while [ "${#}" -gt 0 ]; do
    case "${1}" in
      --writable) mode='rw'; shift ;;
      --image) name="${2:-}"; shift 2 ;;
      *) break ;;
    esac
  done
  validate_args "$@"
  if [ -n "${KEELCORE_DEV_IMAGE:-}" ]; then
    exec "$@"
  fi
  local image
  image="$(dev_image . "${name}")" || exit 1
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

function runtime() {
  if [ "$(uname -s)" = 'Darwin' ]; then printf 'container'; else printf 'docker'; fi
}

# dev_image DIR NAME: the reference for DIR's NAME image — the pinned image.ref when it
# matches the current build context, else the published image of that context (the
# dev-image workflow publishes each context once), else a local build of it.
function dev_image() {
  local -r dir="${1}" name="${2}"
  local -r cf="$(dev_containerfile "${name}")"
  [ -f "${dir}/${cf}" ] || { log "❌ ${dir}/${cf} not found"; exit 1; }
  local base='' hash ref tag
  if dev_has_base "${dir}" "${name}"; then
    base="$(dev_image "${dir}/.standards" dev)" || return 1
  fi
  hash="$(dev_context_hash "${dir}" "${name}" "${base}")"
  if ref="$(dev_pinned_ref "${dir}" "${name}" "${hash}")"; then
    printf '%s' "${ref}"
    return 0
  fi
  if ref="$(dev_published_ref "$(dev_repository "${dir}" "${name}")" "${hash}")"; then
    log "ℹ️  ${dir}/$(dev_ref_file "${name}") pins another build context; using the published ${ref%@*}"
    printf '%s' "${ref}"
    return 0
  fi
  if [ -s "${dir}/$(dev_ref_file "${name}")" ]; then
    # A hook never builds: a commit that changes a build context is checked with the pinned
    # image (the new one is published only after the commit).
    if [ -n "${KEELCORE_DEV_IN_HOOK:-}" ]; then
      ref="$(head -1 "${dir}/$(dev_ref_file "${name}")")"
      log "ℹ️  ${dir}/$(dev_ref_file "${name}") pins another build context (ctx-${hash} is new); hook uses the pinned ${ref%@*}"
      printf '%s' "${ref}"
      return 0
    fi
    log "ℹ️  ${dir}/$(dev_ref_file "${name}") pins another build context; building ctx-${hash} locally"
  elif [ -n "${KEELCORE_DEV_IN_HOOK:-}" ]; then
    log "❌ ${dir}/$(dev_ref_file "${name}") pins no image; a git hook never builds one (make dev-image-publish)"
    exit 1
  fi
  tag="keelcore-${name}-$(dev_repo_name "${dir}"):ctx-${hash}"
  if ! "$(runtime)" image inspect "${tag}" > /dev/null 2>&1; then
    build_image "${dir}" "${cf}" "${tag}" "${base}" || { log "❌ building ${tag} failed"; exit 1; }
  fi
  printf '%s' "${tag}"
}

function build_image() {
  local -r dir="${1}" cf="${2}" tag="${3}" base="${4}"
  local -a args=(build -f "${dir}/${cf}" -t "${tag}")
  [ -z "${base}" ] || args+=(--build-arg "BASE_DEV_IMAGE=${base}")
  if [ "$(runtime)" = 'container' ]; then
    local opt
    dev_ensure_builder
    while IFS= read -r opt; do args+=("${opt}"); done < <(dev_builder_args)
  fi
  log "🔨 Building image ${tag}..."
  "$(runtime)" "${args[@]}" "${dir}" >&2
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

# snapshot DIR: a standalone copy of DIR (a submodule or worktree) under /tmp — a clone (its
# own .git, so git works without the outer repository) holding DIR's current files, tracked
# and untracked; each submodule of DIR is a standalone copy of its own (snapshot_into).
function snapshot() {
  local -r dir="${1}"
  local -r dest="/tmp/keelcore-dev-snapshot/$(dev_repo_name "${dir}")-$(printf '%s' "${dir}" | git hash-object --stdin | cut -c1-12)"
  snapshot_into "${dir}" "${dest}"
  printf '%s' "${dest}"
}

# snapshot_into DIR DEST: make DEST a clone of DIR holding DIR's current files. A submodule's
# own .git is a pointer into its superproject's .git, which the container never sees, so
# submodules are not copied as files: each becomes a snapshot of its own, recursively.
function snapshot_into() {
  local -r dir="${1}" dest="${2}"
  if [ -d "${dest}/.git" ]; then
    git -C "${dest}" fetch --quiet --tags --force "${dir}" HEAD
    git -C "${dest}" reset --quiet --hard FETCH_HEAD
    git -C "${dest}" clean --quiet -fdx
  else
    rm -rf "${dest}"
    mkdir -p "$(dirname "${dest}")"
    git clone --quiet --no-hardlinks "${dir}" "${dest}"
  fi
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

# sync_back SNAPSHOT DIR: copy every file a writable run changed in SNAPSHOT back to DIR.
function sync_back() {
  local -r snap="${1}" dir="${2}"
  local f
  (cd "${snap}" && git ls-files -co --exclude-standard -z) | while IFS= read -r -d '' f; do
    # A submodule entry is a directory, not a file: its own snapshot holds its files.
    [ -d "${snap}/${f}" ] && continue
    cmp -s "${snap}/${f}" "${dir}/${f}" && continue
    mkdir -p "$(dirname "${dir}/${f}")"
    cp -p "${snap}/${f}" "${dir}/${f}"
    log "✍️  ${f}"
  done
}

# run_in IMAGE MODE COMMAND...: COMMAND in IMAGE with this repository mounted at /work (MODE ro
# or rw) and its output directories writable, working directory the same relative path as here.
# A submodule (its git directory lives in the superproject) is mounted as a standalone snapshot
# (/tmp); a writable run's edits are copied back.
function run_in() {
  local -r image="${1}" mode="${2}"
  shift 2
  local here src name
  here="$(git rev-parse --show-toplevel)"
  src="${here}"
  # A submodule or worktree: its .git is a file pointing at a git directory outside it.
  if [ -f "${here}/.git" ]; then
    src="$(snapshot "${here}")"
  fi
  # git reports the physical path (macOS /tmp and /var are symlinks into /private).
  local rel
  rel="$(pwd -P)"
  rel="${rel#"${here}"}"
  local -a args=(run --rm -i -v "${src}:/work:${mode}" -w "/work${rel}")
  args+=(-e "KEELCORE_DEV_REPO=$(dev_repo_name "${here}")")
  # The job's registry login (DOCKER_CONFIG, one login per job), read-only: tools in the image reuse
  # it instead of logging in themselves.
  if [ -n "${DOCKER_CONFIG:-}" ] && [ -d "${DOCKER_CONFIG}" ]; then
    args+=(-v "${DOCKER_CONFIG}:/run/keelcore-docker-config:ro" -e 'DOCKER_CONFIG=/run/keelcore-docker-config')
  fi
  # Apple container caps a container at 1 GB unless told otherwise (docker does not cap).
  [ "$(runtime)" = 'container' ] && args+=(-m "${KEELCORE_DEV_MEMORY:-4g}")
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
  # By name (-e NAME): the runtime copies the value from this process's environment, so no value
  # (tokens included) appears in the runtime's argument list.
  for name in ${PASS_ENV} $(compgen -e | grep -E '^(GITHUB|RUNNER|ACTIONS)_' || true); do
    if [ -n "${!name:-}" ]; then
      export "${name?}"
      args+=(-e "${name}")
    fi
  done
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
  if [ "${src}" != "${here}" ] && [ "${mode}" = 'rw' ]; then
    local rc=0
    "$(runtime)" "${args[@]}" "${image}" "$@" || rc=$?
    sync_back "${src}" "${here}"
    return "${rc}"
  fi
  exec "$(runtime)" "${args[@]}" "${image}" "$@"
}

main "$@"
