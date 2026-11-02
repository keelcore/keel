#!/usr/bin/env bash
# scripts/release/update-pins.sh
# Discovers every pinned external input, resolves the newest eligible candidate
# for each from published metadata only (no binaries are downloaded), and prints
# a report. This is the discovery stage of the pin-refresh process: nothing is
# applied or verified, and the working tree is never touched.
#
# Pin kinds:
#   action     workflow `uses: owner/repo@<sha>  # vX.Y.Z`
#   image      `name:tag@sha256:<digest>` in any repo-owned file
#   tool       `X_VERSION=` in a file that downloads a GitHub release asset
#   gotool     `go install module@version` (TOOL_MODULE/TOOL_VERSION, X_MODULE='m@v')
#   toolchain  go.mod `toolchain goX.Y.Z`
#   gomod      go.mod direct requirements (go.sum + vendor/ follow them)
#   npm        `npm install <pkg>@<version>` (literal or X_VERSION variable)
#   node       .nvmrc
#   unpinned   inputs that float: @latest, apk/apt packages without a version,
#              Homebrew installs, FROM / image: references without a digest
#   unresolved an X_VERSION= pin no adapter recognizes (listed, never skipped)
#
# Eligibility of a candidate: not a prerelease; published at least 14 days ago
# (COOLDOWN_DAYS), or 2 days for a Go toolchain patch release in the same minor
# (GO_PATCH_COOLDOWN_DAYS). A newer major version is reported as `major`
# (report-only); the newest eligible release in the current major is the candidate.
#
# Status: current | stale (eligible candidate exists) | major | cooldown (newer
# release still inside the cooldown) | unpinned | unresolved | hold.
#
# Project hook (run, never sourced; same style as create-release.local.sh):
#   update-pins.local.sh pins    stdout: extra records, kind<TAB>name<TAB>file:line<TAB>current<TAB>source;
#                                a record replaces a generic one at the same location
#   update-pins.local.sh holds   stdout: hold rules, name-regex<TAB>reason
#
# Env: UPDATE_PINS_NOW (epoch seconds; default: now), UPDATE_PINS_FIXTURES (dir;
# every remote read comes from it — tests), GITHUB_TOKEN (else `gh auth token`).
#
# Usage: update-pins.sh

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly COOLDOWN_DAYS=14
readonly GO_PATCH_COOLDOWN_DAYS=2
readonly DAY=86400
readonly LOCAL_HOOK='scripts/release/update-pins.local.sh'
readonly SEMVER_RE='^v?[0-9]+(\.[0-9]+)*$'

NOW=0
GH_TOKEN_CACHE=''
HOLDS=''

# shellcheck source=../lib/paths.sh
. "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  cd "$(git rev-parse --show-toplevel)"
  NOW="${UPDATE_PINS_NOW:-$(date -u +%s)}"
  load_hook
  local records
  records="$(discover)"
  report "$(group "${records}")"
}

# group RECORDS: one line per distinct pin (kind, name, current, source), with
# its first location and the number of locations:
# kind<TAB>name<TAB>location (+N more)<TAB>current<TAB>source
function group() {
  sort -t$'\t' -k1,2 -k4,5 -k3,3V <<< "${1}" | awk -F'\t' -v OFS='\t' '
    NF < 5 { next }
    { key = $1 FS $2 FS $4 FS $5 }
    key != prev { if (prev != "") out(); prev = key; k = $1; n = $2; c = $4; s = $5; loc = $3; cnt = 0 }
    { cnt++ }
    END { if (prev != "") out() }
    function out() { print k, n, (cnt > 1 ? loc " (+" cnt - 1 " more)" : loc), c, s }'
}

function log() {
  printf '%s\n' "${1:-}" >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

function load_hook() {
  [ -f "${LOCAL_HOOK}" ] || return 0
  HOLDS="$(bash "${LOCAL_HOOK}" holds)"
}

# ── discovery ────────────────────────────────────────────────────────────────

# discover: every pin record. A hook record replaces a generic record at
# the same location (the hook knows the project's sources better).
function discover() {
  local generic hooked=''
  generic="$(discover_generic)"
  if [ -f "${LOCAL_HOOK}" ]; then
    hooked="$(bash "${LOCAL_HOOK}" pins)"
  fi
  if [ -z "${hooked}" ]; then
    printf '%s\n' "${generic}"
    return
  fi
  awk -F'\t' 'NR == FNR { if (NF >= 5) hook[$3] = 1; next } !($3 in hook)' \
    <(printf '%s\n' "${hooked}") <(printf '%s\n' "${generic}")
  printf '%s\n' "${hooked}"
}

function discover_generic() {
  discover_actions
  discover_images
  discover_tools
  discover_gotools
  discover_toolchain
  discover_gomods
  discover_npm
  discover_node
  discover_unpinned
}

# record KIND NAME LOCATION CURRENT SOURCE
function record() {
  printf '%s\t%s\t%s\t%s\t%s\n' "${1}" "${2}" "${3}" "${4}" "${5}"
}

# grep_files ERE: file:line:text for each match in the files listed on stdin
# (comment lines skipped).
function grep_files() {
  local -r re="${1}"
  local files=() f
  while IFS= read -r f; do
    [ -n "${f}" ] && files+=("${f}")
  done
  [ "${#files[@]}" -gt 0 ] || return 0
  grep -HnE "${re}" "${files[@]}" 2>/dev/null | grep -vE '^[^:]+:[0-9]+:[[:space:]]*#' || true
}

function files_of() {
  source_files "$@"
}

function discover_actions() {
  local loc ref sha ver
  while IFS= read -r loc; do
    [ -n "${loc}" ] || continue
    ref="$(sed -E 's/.*uses:[[:space:]]*([^@[:space:]]+)@([0-9a-f]{40}).*/\1/' <<< "${loc}")"
    sha="$(sed -E 's/.*@([0-9a-f]{40}).*/\1/' <<< "${loc}")"
    ver="$(sed -nE 's/.*#[[:space:]]*(v?[0-9][^[:space:]]*).*/\1/p' <<< "${loc}")"
    record action "${ref}" "${loc%%:*}:$(cut -d: -f2 <<< "${loc}")" "${ver:-${sha:0:12}}" \
      "github:$(cut -d/ -f1-2 <<< "${ref}")"
  done < <(files_of '.github/workflows/*.yml' '.github/workflows/*.yaml' '.github/actions/*/action.yml' | grep_files 'uses:[[:space:]]*[^.][^@]*@[0-9a-f]{40}')
}

function discover_images() {
  local line file lineno ref
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    file="${line%%:*}"
    lineno="$(cut -d: -f2 <<< "${line}")"
    while IFS= read -r ref; do
      [ -n "${ref}" ] || continue
      record image "${ref%@*}" "${file}:${lineno}" "${ref#*@}" "registry:${ref%@*}"
    done < <(grep -oE '[a-z0-9][a-z0-9._/-]*(:[0-9]+)?/?[a-z0-9._/-]*:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}' <<< "${line#*:*:}")
  done < <(files_of | grep_files '@sha256:[0-9a-f]{64}')
}

# A *_VERSION assignment in a file that downloads from github.com/O/R/releases.
function discover_tools() {
  local file line var val repo
  while IFS= read -r file; do
    repo="$(grep -oE 'github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/releases/download' "${file}" | head -1 \
      | cut -d/ -f2-3)"
    while IFS= read -r line; do
      [ -n "${line}" ] || continue
      var="$(sed -E 's/^[^:]+:[0-9]+:(.*[^A-Z0-9_])?([A-Z0-9_]+_VERSION)=.*/\2/' <<< "${line}")"
      val="$(sed -E "s/^[^:]+:[0-9]+:.*${var}=[\"']?([^\"'[:space:]]+).*/\1/" <<< "${line}")"
      if grep -qE "go install [^ ]*@\\\$\\{?${var}" "${file}"; then
        continue
      elif [ -n "${repo}" ]; then
        record tool "${var%_VERSION}" "${file}:$(cut -d: -f2 <<< "${line}")" "${val}" "github:${repo}"
      elif ! grep -qE "npm (install|i) .*\\\$\\{?${var}" "${file}"; then
        record unresolved "${var%_VERSION}" "${file}:$(cut -d: -f2 <<< "${line}")" "${val}" '-'
      fi
    done < <(printf '%s\n' "${file}" | grep_files '(^|[[:space:]])(readonly |declare -r |local -r |ARG )?[A-Z0-9_]+_VERSION=[^$]' \
      | grep -v 'TOOL_VERSION=')
  done < <(files_of '*.sh' '*Containerfile*' '*Dockerfile*' | xargs grep -lE '[A-Z0-9_]+_VERSION=' 2>/dev/null || true)
}

function discover_gotools() {
  local file mod ver line
  while IFS= read -r file; do
    mod="$(sed -nE "s/.*TOOL_MODULE=[\"']([^\"']+)[\"'].*/\1/p" "${file}" | head -1)"
    ver="$(sed -nE "s/.*TOOL_VERSION=[\"']([^\"']+)[\"'].*/\1/p" "${file}" | head -1)"
    line="$(grep -nE 'TOOL_VERSION=' "${file}" | head -1 | cut -d: -f1)"
    [ -n "${mod}" ] && [ -n "${ver}" ] && record gotool "${mod}" "${file}:${line}" "${ver}" "goproxy:${mod}"
  done < <(files_of '*.sh' | xargs grep -lE 'TOOL_MODULE=' 2>/dev/null || true)
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    mod="$(sed -nE "s/.*_MODULE=[\"']([^\"'@]+)@(v[^\"']+)[\"'].*/\1/p" <<< "${line}")"
    ver="$(sed -nE "s/.*_MODULE=[\"']([^\"'@]+)@(v[^\"']+)[\"'].*/\2/p" <<< "${line}")"
    [ -n "${mod}" ] && record gotool "${mod}" "$(cut -d: -f1-2 <<< "${line}")" "${ver}" "goproxy:${mod}"
  done < <(files_of '*.sh' | grep_files "_MODULE=[\"'][^\"'@]+@v")
  # go install <module>@${X_VERSION}, with X_VERSION assigned in the same file.
  local file var
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    file="${line%%:*}"
    mod="$(sed -nE 's/.*go install "?([^"@[:space:]]+)@\$\{?([A-Z0-9_]+)\}?.*/\1/p' <<< "${line}")"
    var="$(sed -nE 's/.*go install "?([^"@[:space:]]+)@\$\{?([A-Z0-9_]+)\}?.*/\2/p' <<< "${line}")"
    [ -n "${mod}" ] && [ "${var}" != 'TOOL_VERSION' ] || continue
    ver="$(sed -nE "s/.*${var}=[\"']?([^\"'[:space:]]+).*/\1/p" "${file}" | head -1)"
    [ -n "${ver}" ] && record gotool "${mod}" "${file}:$(grep -nE "${var}=" "${file}" | head -1 | cut -d: -f1)" \
      "${ver}" "goproxy:${mod}"
  done < <(files_of '*.sh' | grep_files 'go install [^ ]*@\$\{?[A-Z0-9_]+')
}

function discover_toolchain() {
  local line
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    record toolchain go "$(cut -d: -f1-2 <<< "${line}")" "$(awk '{print $2}' <<< "${line#*:*:}")" 'golang-toolchain'
  done < <(files_of 'go.mod' '*/go.mod' | grep_files '^toolchain go[0-9]')
}

# Direct requirements of every repo-owned go.mod (go.sum and vendor/ follow them).
function discover_gomods() {
  local gomod
  while IFS= read -r gomod; do
    [ -n "${gomod}" ] || continue
    awk -v f="${gomod}" '
      /^require[[:space:]]*\(/ { blk = 1; next }
      blk && /^\)/             { blk = 0; next }
      /\/\/ indirect/          { next }
      blk && NF >= 2           { printf "gomod\t%s\t%s:%d\t%s\tgoproxy:%s\n", $1, f, NR, $2, $1; next }
      /^require[[:space:]]+[^(]/ { printf "gomod\t%s\t%s:%d\t%s\tgoproxy:%s\n", $2, f, NR, $3, $2 }
    ' "${gomod}"
  done < <(files_of 'go.mod' '*/go.mod')
}

function discover_npm() {
  local line file pkg ver var
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    file="${line%%:*}"
    while IFS= read -r pkg; do
      [ -n "${pkg}" ] || continue
      ver="${pkg##*@}"
      pkg="${pkg%@*}"
      if [[ "${ver}" == \$* ]]; then
        var="${ver//[\$\{\}]/}"
        ver="$(sed -nE "s/.*${var}=[\"']?([^\"'[:space:]]+).*/\1/p" "${file}" | head -1)"
      fi
      record npm "${pkg}" "$(cut -d: -f1-2 <<< "${line}")" "${ver}" "npm:${pkg}"
    done < <(grep -oE '[@a-z0-9][a-z0-9._/-]*@(\$\{?[A-Z0-9_]+\}?|[0-9][0-9.]*)' <<< "${line#*:*:}")
  done < <(files_of '*.sh' '*.yml' '*.yaml' | grep_files 'npm (install|i) ')
}

function discover_node() {
  local f
  while IFS= read -r f; do
    [ -n "${f}" ] || continue
    record node node "${f}:1" "$(tr -d '[:space:]' < "${f}")" 'nodejs'
  done < <(files_of '.nvmrc' '*/.nvmrc')
}

function discover_unpinned() {
  local line
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    record unpinned "$(grep -oE '[^[:space:]"]+@latest' <<< "${line}" | head -1)" "$(cut -d: -f1-2 <<< "${line}")" '@latest' 'floats'
  done < <(files_of '*.sh' 'Makefile' '*.mk' '*.yml' '*.yaml' '*Containerfile*' '*Dockerfile*' | grep_files '@latest')
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    record unpinned "apk:$(sed -E 's/.*apk add[[:space:]]+(--[a-z-]+[[:space:]]+)*//' <<< "${line}" | tr -s ' ' ',')" \
      "$(cut -d: -f1-2 <<< "${line}")" 'no version' 'floats'
  done < <(files_of '*Containerfile*' '*Dockerfile*' '*.sh' | grep_files 'apk add' | grep -vE 'apk add[^#]*=[0-9]')
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    record unpinned "apt:$(sed -E 's/.*apt(-get)? install[[:space:]]+(-[a-zA-Z-]+[[:space:]]+)*//; s/[[:space:]]+--.*//' <<< "${line}" | tr -s ' ' ',')" \
      "$(cut -d: -f1-2 <<< "${line}")" 'no version' 'floats'
  done < <(files_of '*.sh' '*Containerfile*' '*Dockerfile*' | grep_files 'apt(-get)? install' | grep -v "log '" | grep -vE 'install[^#]*=[0-9]')
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    record unpinned "brew:$(sed -E "s/.*brew install[[:space:]]+([^;|&\"')]+).*/\1/" <<< "${line}" | xargs | tr ' ' ',')" \
      "$(cut -d: -f1-2 <<< "${line}")" 'no version' 'floats (Homebrew)'
  done < <(files_of '*.sh' | grep_files 'brew install [a-z]' | grep -v "log '\|printf ")
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    record unpinned "$(awk '{print $2}' <<< "${line#*:*:}")" "$(cut -d: -f1-2 <<< "${line}")" 'no digest' 'floats'
  done < <(files_of '*Containerfile*' '*Dockerfile*' | grep_files '^[[:space:]]*FROM[[:space:]]' \
    | grep -v '@sha256:' | grep -vE 'FROM[[:space:]]+(--platform=[^[:space:]]+[[:space:]]+)?(scratch|\$\{)')
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    record unpinned "$(sed -E 's/.*image:[[:space:]]*["'\'']?([^"'\''[:space:]]+).*/\1/' <<< "${line}")" \
      "$(cut -d: -f1-2 <<< "${line}")" 'no digest' 'floats'
  done < <(files_of '*.yml' '*.yaml' | grep_files '^[[:space:]]*(-[[:space:]]+)?image:[[:space:]]*[a-z]' \
    | grep -v '@sha256:' | grep -v '{{')
}

# ── resolution ───────────────────────────────────────────────────────────────

function fixture_key() {
  printf '%s' "${1}" | tr -c 'A-Za-z0-9._-' '_'
}

# fetch URL: body of an HTTP GET (GitHub API requests are authenticated).
function fetch() {
  local -r url="${1}"
  if [ -n "${UPDATE_PINS_FIXTURES:-}" ]; then
    cat "${UPDATE_PINS_FIXTURES}/$(fixture_key "${url}")" 2>/dev/null
    return
  fi
  if [[ "${url}" == https://api.github.com/* ]]; then
    curl -fsL --retry 3 --retry-delay 2 --max-time 60 -H "Authorization: Bearer $(github_token)" \
      -H 'Accept: application/vnd.github+json' "${url}"
  else
    curl -fsL --retry 3 --retry-delay 2 --max-time 60 "${url}"
  fi
}

function github_token() {
  if [ -z "${GH_TOKEN_CACHE}" ]; then
    GH_TOKEN_CACHE="${GITHUB_TOKEN:-$(gh auth token 2>/dev/null || true)}"
  fi
  printf '%s' "${GH_TOKEN_CACHE}"
}

# versions_github O/R: "<tag>\t<epoch>" for every published, non-draft release.
function versions_github() {
  local body
  body="$(fetch "https://api.github.com/repos/${1}/releases?per_page=100")" || { failed_lookup; return 0; }
  jq -r '.[] | select(.draft | not) | select(.prerelease | not)
         | "\(.tag_name)\t\(.published_at | sub("\\.[0-9]+"; "") | fromdateiso8601)"' <<< "${body}" 2>/dev/null \
    || failed_lookup
}

# failed_lookup: the in-band marker choose() turns into "unresolved".
function failed_lookup() {
  printf '!fail\t0\n'
}

# goproxy_escape MODULE: the proxy's case encoding (Upper → !lower).
function goproxy_escape() {
  awk '{ o = ""; for (i = 1; i <= length($0); i++) { c = substr($0, i, 1); o = o (c ~ /[A-Z]/ ? "!" tolower(c) : c) } print o }' <<< "${1}"
}

# goproxy_root PATH: the longest prefix of PATH the proxy serves as a module.
function goproxy_root() {
  local path="${1}"
  while [ -n "${path}" ] && [[ "${path}" == */* ]]; do
    if [ -n "$(fetch "https://proxy.golang.org/$(goproxy_escape "${path}")/@v/list")" ]; then
      printf '%s' "${path}"
      return 0
    fi
    path="${path%/*}"
  done
  return 1
}

# versions_goproxy MODULE CURRENT: "<version>\t<epoch>" for releases newer than CURRENT.
function versions_goproxy() {
  local -r mod="${1}" current="${2}"
  local root esc v t
  root="$(goproxy_root "${mod}")" || { failed_lookup; return 0; }
  esc="$(goproxy_escape "${root}")"
  while IFS= read -r v; do
    [ -n "${v}" ] || continue
    [[ "${v}" =~ ${SEMVER_RE} ]] || continue
    newer "${v}" "${current}" || continue
    t="$(fetch "https://proxy.golang.org/${esc}/@v/${v}.info" | jq -r '.Time | sub("\\.[0-9]+"; "") | fromdateiso8601' 2>/dev/null || true)"
    if [ -n "${t}" ]; then printf '%s\t%s\n' "${v}" "${t}"; else failed_lookup; fi
  done < <(fetch "https://proxy.golang.org/${esc}/@v/list" | sed 's/+incompatible$//')
}

# versions_toolchain CURRENT: "<goX.Y.Z>\t<epoch>" for Go releases newer than CURRENT.
function versions_toolchain() {
  local -r current="${1}"
  local v t list
  list="$(fetch 'https://proxy.golang.org/golang.org/toolchain/@v/list')" || { failed_lookup; return 0; }
  while IFS= read -r v; do
    [ -n "${v}" ] || continue
    newer "${v#go}" "${current#go}" || continue
    t="$(fetch "https://proxy.golang.org/golang.org/toolchain/@v/v0.0.1-${v}.linux-amd64.info" \
      | jq -r '.Time | sub("\\.[0-9]+"; "") | fromdateiso8601' 2>/dev/null || true)"
    if [ -n "${t}" ]; then printf '%s\t%s\n' "${v}" "${t}"; else failed_lookup; fi
  done < <(sed -nE 's/^v0\.0\.1-(go[0-9]+\.[0-9]+(\.[0-9]+)?)\.linux-amd64$/\1/p' <<< "${list}" | sort -u)
}

function versions_npm() {
  local body
  body="$(fetch "https://registry.npmjs.org/${1}")" || { failed_lookup; return 0; }
  jq -r '.time | to_entries[] | select(.key | test("^[0-9]+\\.[0-9]+\\.[0-9]+$"))
         | "\(.key)\t\(.value | sub("\\.[0-9]+"; "") | fromdateiso8601)"' <<< "${body}" 2>/dev/null \
    || failed_lookup
}

# versions_node MAJOR: "<vX.Y.Z>\t<epoch>" for LTS releases in that major.
function versions_node() {
  local body
  body="$(fetch 'https://nodejs.org/dist/index.json')" || { failed_lookup; return 0; }
  jq -r --arg m "v${1%%.*}." '.[] | select(.lts) | select(.version | startswith($m))
         | "\(.version)\t\(.date + "T00:00:00Z" | fromdateiso8601)"' <<< "${body}" 2>/dev/null \
    || failed_lookup
}

function image_digest() {
  local -r ref="${1}"
  if [ -n "${UPDATE_PINS_FIXTURES:-}" ]; then
    cat "${UPDATE_PINS_FIXTURES}/$(fixture_key "digest:${ref}")" 2>/dev/null
    return
  fi
  local name="${ref%:*}" tag="${ref##*:}" registry repo token='' auth=()
  registry="${name%%/*}"
  if [[ "${name}" != */* ]] || [[ "${registry}" != *.* && "${registry}" != *:* ]]; then
    registry='registry-1.docker.io'
    repo="${name#docker.io/}"
    [[ "${repo}" == */* ]] || repo="library/${repo}"
  else
    repo="${name#*/}"
    [ "${registry}" = 'docker.io' ] && registry='registry-1.docker.io'
  fi
  case "${registry}" in
    registry-1.docker.io)
      token="$(curl -fsSL --max-time 20 "https://auth.docker.io/token?service=registry.docker.io&scope=repository:${repo}:pull" | jq -r .token)" ;;
    ghcr.io)
      token="$(curl -fsSL --max-time 20 "https://ghcr.io/token?scope=repository:${repo}:pull" | jq -r .token)" ;;
  esac
  [ -n "${token}" ] && auth=(-H "Authorization: Bearer ${token}")
  curl -fsSI --max-time 20 ${auth[@]+"${auth[@]}"} \
    -H 'Accept: application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json' \
    "https://${registry}/v2/${repo}/manifests/${tag}" \
    | tr -d '\r' | awk 'tolower($1) == "docker-content-digest:" { print $2 }'
}

# newer A B: A is a strictly newer version than B (leading v/go ignored).
function newer() {
  local a="${1#v}" b="${2#v}"
  a="${a#go}"
  b="${b#go}"
  [ "${a}" != "${b}" ] && [ "$(printf '%s\n%s\n' "${a}" "${b}" | sort -V | tail -1)" = "${a}" ]
}

# major_of VERSION: the compatibility line — the major, or "0.<minor>" for 0.x
# releases (semver: before 1.0 a minor bump may break).
function major_of() {
  local v="${1#v}"
  v="${v#go}"
  if [ "${v%%.*}" = '0' ]; then
    local rest="${v#0.}"
    printf '0.%s' "${rest%%.*}"
  else
    printf '%s' "${v%%.*}"
  fi
}

# choose CURRENT COOLDOWN_DAYS: reads "<version>\t<epoch>" lines; prints
# "<status>\t<candidate>\t<age_days>\t<note>".
function choose() {
  local -r current="${1}" cooldown="${2}"
  local v t best='' best_t=0 fresh='' fresh_t=0 major='' age failed=''
  while IFS=$'\t' read -r v t; do
    [ "${v}" = '!fail' ] && failed=1
    [ -n "${v}" ] && [ -n "${t}" ] || continue
    [[ "${v}" =~ ${SEMVER_RE} || "${v}" =~ ^go[0-9.]+$ ]] || continue
    newer "${v}" "${current}" || continue
    if [ "$(major_of "${v}")" != "$(major_of "${current}")" ]; then
      { [ -z "${major}" ] || newer "${v}" "${major}"; } && major="${v}"
      continue
    fi
    if [ $(( (NOW - t) / DAY )) -ge "$(cooldown_for "${v}" "${current}" "${cooldown}")" ]; then
      { [ -z "${best}" ] || newer "${v}" "${best}"; } && { best="${v}"; best_t="${t}"; }
    else
      { [ -z "${fresh}" ] || newer "${v}" "${fresh}"; } && { fresh="${v}"; fresh_t="${t}"; }
    fi
  done
  if [ -n "${failed}" ]; then
    printf 'unresolved\t-\t-\tlookup failed; candidates unknown\n'
    return
  fi
  local note=''
  [ -n "${major}" ] && note="major ${major} available (report-only)"
  if [ -n "${best}" ]; then
    age=$(( (NOW - best_t) / DAY ))
    printf 'stale\t%s\t%sd\t%s\n' "${best}" "${age}" "${note}"
  elif [ -n "${fresh}" ]; then
    age=$(( (NOW - fresh_t) / DAY ))
    printf 'cooldown\t%s\t%sd\t%s\n' "${fresh}" "${age}" "${note:-inside the cooldown}"
  elif [ -n "${major}" ]; then
    printf 'major\t%s\t-\t%s\n' "${major}" "${note}"
  else
    printf 'current\t-\t-\t\n'
  fi
}

# cooldown_for CANDIDATE CURRENT DEFAULT: Go toolchain patch releases in the
# same minor use the short security cooldown.
function cooldown_for() {
  local -r cand="${1}" current="${2}" default="${3}"
  if [[ "${cand}" == go* ]]; then
    local cm="${cand#go}" om="${current#go}"
    if [ "${cm%.*}" = "${om%.*}" ]; then
      printf '%s' "${GO_PATCH_COOLDOWN_DAYS}"
      return
    fi
  fi
  printf '%s' "${default}"
}

# resolve KIND NAME CURRENT SOURCE: "<status>\t<candidate>\t<age>\t<note>".
function resolve() {
  local -r kind="${1}" name="${2}" current="${3}" source="${4}"
  case "${kind}" in
    action | tool)
      versions_github "${source#github:}" | choose "${current}" "${COOLDOWN_DAYS}" ;;
    gotool | gomod)
      versions_goproxy "${source#goproxy:}" "${current}" | choose "${current}" "${COOLDOWN_DAYS}" ;;
    toolchain)
      versions_toolchain "${current}" | choose "${current}" "${COOLDOWN_DAYS}" ;;
    npm)
      versions_npm "${source#npm:}" | choose "${current}" "${COOLDOWN_DAYS}" ;;
    node)
      resolve_node "${current}" ;;
    image)
      resolve_image "${name}" "${current}" ;;
    unpinned)
      printf 'unpinned\t-\t-\t%s\n' "${source}" ;;
    *)
      printf 'unresolved\t-\t-\tno adapter for this pin\n' ;;
  esac
}

function resolve_node() {
  local -r current="${1}"
  if [[ "${current}" =~ ^v?[0-9]+$ ]]; then
    local latest
    latest="$(versions_node "${current#v}" | grep -v '^!fail' | sort -t$'\t' -k1,1V | tail -1 | cut -f1)"
    printf 'unpinned\t%s\t-\tmajor only; pin the exact LTS release\n' "${latest:--}"
    return
  fi
  versions_node "${current#v}" | choose "${current}" "${COOLDOWN_DAYS}"
}

function resolve_image() {
  local -r ref="${1}" current="${2}"
  local digest
  digest="$(image_digest "${ref}" || true)"
  if [ -z "${digest}" ]; then
    printf 'unresolved\t-\t-\tregistry lookup failed\n'
  elif [ "${digest}" = "${current}" ]; then
    printf 'current\t-\t-\t\n'
  else
    printf 'stale\t%s\t-\ttag %s now points at a new digest\n' "${digest:0:19}…" "${ref##*:}"
  fi
}

# held NAME: the hold reason for NAME, if a hook hold rule matches.
function held() {
  local re reason
  while IFS=$'\t' read -r re reason; do
    [ -n "${re}" ] || continue
    if grep -qE "${re}" <<< "${1}"; then
      printf '%s' "${reason}"
      return 0
    fi
  done <<< "${HOLDS}"
  return 1
}

# ── report ───────────────────────────────────────────────────────────────────

function report() {
  local -r records="${1}"
  local kind name loc current source status cand age note reason rows='' total=0 statuses=''
  while IFS=$'\t' read -r kind name loc current source; do
    [ -n "${kind}" ] || continue
    if reason="$(held "${name}")"; then
      status='hold'
      cand='-'
      age='-'
      note="${reason}"
    else
      IFS=$'\t' read -r status cand age note < <(resolve "${kind}" "${name}" "${current}" "${source}")
    fi
    statuses+="${status}"$'\n'
    total=$(( total + 1 ))
    rows+="$(printf '%-10s %-44s %-14s %-14s %-5s %-10s %s  (%s)' "${kind}" "${name:0:44}" "${current:0:14}" \
      "${cand:0:14}" "${age}" "${status}" "${note}" "${loc}")"$'\n'
  done <<< "${records}"
  local summary='' s n
  for s in current stale cooldown major unpinned unresolved hold; do
    n="$(grep -cx "${s}" <<< "${statuses}" || true)"
    [ "${n}" -gt 0 ] && summary+="${n} ${s}, "
  done
  log "update-pins (discovery) — ${total} pins: ${summary%, }"
  log "$(printf '%-10s %-44s %-14s %-14s %-5s %-10s %s' KIND PIN CURRENT CANDIDATE AGE STATUS 'NOTE  (LOCATION)')"
  printf "%s" "${rows}" | sort >&5
}

main "${@:-}"
