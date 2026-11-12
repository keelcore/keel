#!/usr/bin/env bash
# ensure-keelcore.sh
# keelcore's own releases take no update-pins cooldown. `make update-pins` runs this first
# (make ensure-keelcore), so the canonical update-pins then finds them current:
#   - each github.com/keelcore/* module go.mod requires moves to its latest release;
#   - each `FROM golang:<version>[-<variant>]@sha256:` base image moves to the Go release go.mod's
#     toolchain names, by that tag's digest.
#
# Usage: ensure-keelcore.sh [--apply]
#   (no args)  report each one behind; nothing is written
#   --apply    write them: go get, tidy and vendor (Go's output goes to the log), and each FROM line.
#              Configuration only: nothing is built or run. A failure restores every file it changed;
#              it refuses while any of those files has uncommitted changes, so the restore loses nothing.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly LOG_FILE='/tmp/keel_ensure_keelcore.log'
readonly PROXY='https://proxy.golang.org'

# Pending changes: "module<TAB>version" and "file<TAB>line<TAB>old ref<TAB>new tag".
MODULES=''
IMAGES=''

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local apply=''
  [ "${1:-}" != '--apply' ] || apply=1
  cd "$(dirname "${BASH_SOURCE[0]}")/../.."
  check_modules
  check_images
  [ -n "${apply}" ] || return 0
  if [ -z "${MODULES}${IMAGES}" ]; then
    log '✅ keelcore: nothing to apply'
    return 0
  fi
  apply_all
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a "${LOG_FILE}" >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || { [ -n "${1:-}" ] && [ "${1}" != '--apply' ]; }; then
    log '❌ Error: Unexpected argument (usage: ensure-keelcore.sh [--apply])'
    exit 1
  fi
}

# keelcore_modules: "module<TAB>version" for each github.com/keelcore/* module go.mod requires.
function keelcore_modules() {
  awk '
    /^require \($/ { block = 1; next }
    block && /^\)/ { block = 0; next }
    block && $1 ~ /^github\.com\/keelcore\// { print $1 "\t" $2 }
    !block && $1 == "require" && $2 ~ /^github\.com\/keelcore\// { print $2 "\t" $3 }
  ' go.mod
}

# latest_module MODULE: the newest release (no prerelease) the module proxy lists for MODULE.
function latest_module() {
  curl -fsSL --max-time 20 "${PROXY}/${1}/@v/list" | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -1
}

function check_modules() {
  local module current latest
  while IFS=$'\t' read -r module current; do
    [ -n "${module}" ] || continue
    latest="$(latest_module "${module}" || true)"
    if [ -z "${latest}" ]; then
      log "❌ ${module}: the module proxy has no latest release"
      exit 1
    fi
    if [ "${latest}" != "${current}" ] && [ "$(printf '%s\n%s\n' "${current#v}" "${latest#v}" | sort -V | tail -1)" = "${latest#v}" ]; then
      log "🔍 ${module} ${current} → ${latest} (keelcore: no cooldown)"
      MODULES+="${module}"$'\t'"${latest}"$'\n'
    else
      log "✅ ${module} ${current} current"
    fi
  done < <(keelcore_modules)
}

function check_images() {
  local toolchain match file line ref version variant
  toolchain="$(sed -nE 's/^toolchain go([0-9][0-9.]*)$/\1/p' go.mod)"
  [ -n "${toolchain}" ] || return 0
  while IFS= read -r match; do
    [ -n "${match}" ] || continue
    file="${match%%:*}"
    line="$(cut -d: -f2 <<< "${match}")"
    ref="$(sed -E 's/^[^:]+:[0-9]+:FROM[[:space:]]+(golang:[^[:space:]@]+)@.*/\1/' <<< "${match}")"
    version="$(sed -E 's/^golang:([0-9][0-9.]*).*/\1/' <<< "${ref}")"
    variant="${ref#"golang:${version}"}"
    if [ "${version}" = "${toolchain}" ]; then
      log "✅ ${file}:${line} ${ref} current"
    else
      log "🔍 ${file}:${line} ${ref} → golang:${toolchain}${variant} (go.mod toolchain go${toolchain})"
      IMAGES+="${file}"$'\t'"${line}"$'\t'"${ref}"$'\t'"${toolchain}${variant}"$'\n'
    fi
  done < <(git ls-files '*Containerfile*' '*Dockerfile*' | xargs grep -nE '^FROM[[:space:]]+golang:[0-9][^[:space:]]*@sha256:' /dev/null || true)
}

# image_digest TAG: Docker Hub's digest for golang:TAG.
function image_digest() {
  local token
  token="$(curl -fsSL --max-time 20 'https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/golang:pull' \
    | sed -nE 's/.*"token":"([^"]+)".*/\1/p')"
  curl -fsI --max-time 20 -H "Authorization: Bearer ${token}" \
    -H 'Accept: application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json' \
    "https://registry-1.docker.io/v2/library/golang/manifests/${1}" \
    | tr -d '\r' | awk 'tolower($1) == "docker-content-digest:" { print $2 }'
}

# apply_all: refuse on uncommitted changes, resolve every digest, then write; restore on failure.
function apply_all() {
  local -a paths=()
  local file line ref tag digest resolved='' dirty
  [ -z "${MODULES}" ] || paths+=(go.mod go.sum vendor)
  while IFS=$'\t' read -r file line ref tag; do
    [ -n "${file}" ] || continue
    paths+=("${file}")
    digest="$(image_digest "${tag}" || true)"
    if ! [[ "${digest}" =~ ^sha256:[0-9a-f]{64}$ ]]; then
      log "❌ no digest for golang:${tag}"
      exit 1
    fi
    resolved+="${file}"$'\t'"${line}"$'\t'"${ref}"$'\t'"golang:${tag}@${digest}"$'\n'
  done <<< "${IMAGES}"
  dirty="$(git status --porcelain -- "${paths[@]}")"
  if [ -n "${dirty}" ]; then
    log "❌ uncommitted changes in files ensure-keelcore would change; commit or stash them first:"
    log "${dirty}"
    exit 1
  fi
  if write_all "${resolved}"; then
    log '✅ keelcore: applied'
    return 0
  fi
  git checkout -- "${paths[@]}"
  [ -z "${MODULES}" ] || git clean -fdq -- vendor
  log "❌ keelcore: the update failed (log: ${LOG_FILE}); restored ${paths[*]}"
  exit 1
}

# write_all RESOLVED: go get, tidy and vendor the modules (output to the log), then rewrite each image line.
function write_all() {
  local -r resolved="${1}"
  local file line ref new
  local -a specs=()
  local module version
  while IFS=$'\t' read -r module version; do
    [ -n "${module}" ] && specs+=("${module}@${version}")
  done <<< "${MODULES}"
  if [ "${#specs[@]}" -gt 0 ]; then
    { GOFLAGS=-mod=mod go get "${specs[@]}" && go mod tidy && go mod vendor; } >> "${LOG_FILE}" 2>&1 || return 1
    log "📝 go.mod, go.sum, vendor/ → ${specs[*]}"
  fi
  while IFS=$'\t' read -r file line ref new; do
    [ -n "${file}" ] || continue
    LINE="${line}" REF="${ref}" NEW="${new}" \
      perl -pi -e '$. == $ENV{LINE} and s/^(FROM\s+)\Q$ENV{REF}\E\@sha256:[0-9a-f]{64}/$1$ENV{NEW}/' "${file}" || return 1
    grep -qF "FROM ${new}" "${file}" || { log "❌ ${file}:${line} not rewritten"; return 1; }
    log "📝 ${file}:${line} → ${new}"
  done <<< "${resolved}"
}

main "${@:-}"
