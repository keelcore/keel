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
