#!/usr/bin/env bash
# scripts/release/update-pins.sh
# Discovers every pinned external input, resolves the newest eligible candidate
# for each from published metadata only (no binaries are downloaded), prints a
# report, then verifies every stale pin: applied alone in a throwaway git
# worktree, exercised by the make targets mapped to its kind, rolled back. A
# failing pin never stops the others. Every passing pin is then applied together
# for one combined run (lint, audit, test, plus project extras).
#
# Modes:
#   (default)        discovery + per-pin verification + combined run; the working
#                    tree is never touched
#   --apply          as default, then write the passing pins to the working tree
#                    (only when the combined run passed) for review and staging
#   --discover-only  the discovery report alone
#   --tsv FILE       also write the resolved pins (TSV) to FILE
#   --report FILE    also write the human-readable report to FILE
# Exit status: 1 when a pin or the combined run failed, else 0.
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
#   update-pins.local.sh verify KIND NAME FILE PLATFORM
#                                stdout: make targets that verify that pin, or
#                                "verify-on-ci"; empty = the default for KIND
#   update-pins.local.sh combined PLATFORM
#                                stdout: make targets for the combined run, replacing
#                                the default (lint audit test); empty = default
#   update-pins.local.sh cleanup PLATFORM
#                                release anything the verification runs created
#   update-pins.local.sh apply ROOT KIND NAME CURRENT CANDIDATE LOCATIONS
#                                write the pin itself: exit 0 done, 3 not handled
#
# Env: UPDATE_PINS_NOW (epoch seconds; default: now), UPDATE_PINS_FIXTURES (dir;
# every remote read comes from it — tests), UPDATE_PINS_PLATFORM (default:
# uname -s), UPDATE_PINS_LOGDIR (per-pin verification logs; default: next to the
# throwaway worktree), GITHUB_TOKEN (else `gh auth token`).
#
# Usage: update-pins.sh [--apply | --discover-only] [--tsv FILE] [--report FILE]

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
ROOT=''
MODE='verify'
APPLY=''
TSV_OUT=''
REPORT_OUT=''
PLATFORM=''
WT=''
LOGDIR=''
TOOLS=''

# shellcheck source=../lib/paths.sh
. "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  ROOT="$(git rev-parse --show-toplevel)"
  cd "${ROOT}"
  NOW="${UPDATE_PINS_NOW:-$(date -u +%s)}"
  PLATFORM="${UPDATE_PINS_PLATFORM:-$(uname -s)}"
  if [ -n "${REPORT_OUT}" ]; then
    mkdir -p "$(dirname "${REPORT_OUT}")"
    : > "${REPORT_OUT}"
    exec 5> >(tee -a "${REPORT_OUT}")
  fi
  load_hook
  local resolved
  resolved="$(dependabot_union "$(resolve_all "$(group "$(discover)")")")"
  report "${resolved}"
  [ -z "${TSV_OUT}" ] || write_tsv "${resolved}" > "${TSV_OUT}"
  [ "${MODE}" = 'discover' ] && return 0
  verify_all "${resolved}"
}

# group RECORDS: one line per distinct pin (kind, name, current, source):
# kind<TAB>name<TAB>first location (+N more)<TAB>current<TAB>source<TAB>all locations (comma-separated)
function group() {
  sort -t$'\t' -k1,2 -k4,5 -k3,3V <<< "${1}" | awk -F'\t' -v OFS='\t' '
    NF < 5 { next }
    { key = $1 FS $2 FS $4 FS $5 }
    key != prev { if (prev != "") out(); prev = key; k = $1; n = $2; c = $4; s = $5; loc = $3; all = ""; cnt = 0 }
    { cnt++; all = all (all == "" ? "" : ",") $3 }
    END { if (prev != "") out() }
    function out() { print k, n, (cnt > 1 ? loc " (+" cnt - 1 " more)" : loc), c, s, all }'
}

function log() {
  printf '%s\n' "${1:-}" >&5
}

# validate_args: --discover-only (report only), --apply (write passing pins to
# the working tree), --tsv FILE (machine-readable resolution, for create-release).
function validate_args() {
  while [ "${#}" -gt 0 ]; do
    case "${1}" in
      '') ;;
      --discover-only) MODE='discover' ;;
      --apply) APPLY=1 ;;
      --tsv)
        [ -n "${2:-}" ] || { log '❌ Error: --tsv needs a file'; exit 1; }
        TSV_OUT="${2}"
        shift ;;
      --report)
        [ -n "${2:-}" ] || { log '❌ Error: --report needs a file'; exit 1; }
        REPORT_OUT="${2}"
        shift ;;
      *)
        log "❌ Error: Unexpected argument: ${1}"
        exit 1 ;;
    esac
    shift
  done
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

# files_of GLOB...: repo-owned files, minus this driver (it quotes every pattern it
# looks for).
function files_of() {
  source_files "$@" | grep_exclude -vxF 'scripts/release/update-pins.sh'
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
  local line file pkg ver var loc
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    file="${line%%:*}"
    while IFS= read -r pkg; do
      [ -n "${pkg}" ] || continue
      ver="${pkg##*@}"
      pkg="${pkg%@*}"
      loc="$(cut -d: -f1-2 <<< "${line}")"
      if [[ "${ver}" == \$* ]]; then
        # The pin lives where the variable is assigned, not where it is used.
        var="${ver//[\$\{\}]/}"
        ver="$(sed -nE "s/.*${var}=[\"']?([^\"'[:space:]]+).*/\1/p" "${file}" | head -1)"
        loc="${file}:$(grep -nE "${var}=" "${file}" | head -1 | cut -d: -f1)"
      fi
      record npm "${pkg}" "${loc}" "${ver}" "npm:${pkg}"
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
    printf 'stale\t%s\t-\ttag %s now points at a new digest\n' "${digest}" "${ref##*:}"
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

# ── Dependabot union ─────────────────────────────────────────────────────────
# The recommendation set is the union of update-pins' own resolution and
# Dependabot's (open PRs and alerts). Each resolved pin gains an 11th field,
# its SOURCE: update-pins | dependabot | both (stale pins), else "-". Where the
# two disagree, the newer candidate is taken; the verification loop treats both
# alike.

# repo_slug: owner/repo of the first GitHub remote.
function repo_slug() {
  git remote -v 2>/dev/null | sed -nE 's#.*github\.com[:/]([^/[:space:]]+/[^/[:space:]]+)[[:space:]].*#\1#p' \
    | sed 's/\.git$//' | head -1
}

# dependabot_suggestions: "<name>\t<version>\t<origin>" for each open Dependabot
# PR ("bump X from A to B") and each open alert with a patched version.
function dependabot_suggestions() {
  local slug body
  slug="$(repo_slug)"
  [ -n "${slug}" ] || return 0
  if body="$(fetch "https://api.github.com/repos/${slug}/pulls?state=open&per_page=100")"; then
    jq -r '.[] | select(.user.login == "dependabot[bot]") | "\(.number)\t\(.title)"' <<< "${body}" 2>/dev/null \
      | sed -nE 's/^([0-9]+)\t.*[Bb]ump ([^[:space:]]+) from ([^[:space:]]+) to ([^[:space:]]+).*/\2\t\4\tPR #\1/p'
  fi
  if body="$(fetch "https://api.github.com/repos/${slug}/dependabot/alerts?state=open&per_page=100")"; then
    jq -r '.[] | select(.security_vulnerability.first_patched_version.identifier != null)
           | "\(.dependency.package.name)\t\(.security_vulnerability.first_patched_version.identifier)\talert #\(.number)"' \
      <<< "${body}" 2>/dev/null
  fi
}

# suggestion_for KIND NAME SUGGESTIONS: the newest suggested "<version>\t<origin>"
# for a pin. Names match exactly, as an action sub-path (owner/repo/sub), or as
# an image's repository without tag (golang:1.27.3 ↔ golang).
function suggestion_for() {
  local -r kind="${1}" name="${2}" suggestions="${3}"
  local key="${name}" sname sver sorigin best='' best_origin=''
  [ "${kind}" = 'image' ] && key="$(sed -E 's#^(docker\.io/)?(library/)?##; s#:[^:/]*$##' <<< "${name}")"
  while IFS=$'\t' read -r sname sver sorigin; do
    [ -n "${sname}" ] || continue
    [ "${sname}" = "${key}" ] || [[ "${key}" == "${sname}/"* ]] || continue
    { [ -z "${best}" ] || newer "${sver}" "${best}"; } && { best="${sver}"; best_origin="${sorigin}"; }
  done <<< "${suggestions}"
  [ -n "${best}" ] && printf '%s\t%s' "${best}" "${best_origin}"
}

function dependabot_union() {
  local -r resolved="${1}"
  local suggestions kind name loc current source status cand age note locs sug sver sorigin origin
  suggestions="$(dependabot_suggestions)"
  while IFS=$'\t' read -r kind name loc current source status cand age note locs; do
    [ -n "${kind}" ] || continue
    origin='-'
    [ "${status}" = 'stale' ] && origin='update-pins'
    sug="$(suggestion_for "${kind}" "${name}" "${suggestions}")"
    if [ -n "${sug}" ]; then
      IFS=$'\t' read -r sver sorigin <<< "${sug}"
      # Dependabot drops the "v" the pin may carry; keep the pin's own style.
      [[ "${current}" == v* && "${sver}" != v* ]] && sver="v${sver}"
      # An image suggestion changes the tag; the image adapter refreshes digests
      # only, so it stays a note (report-only).
      [ "${kind}" = 'image' ] && status="note:${status}"
      case "${status}" in
        hold | unpinned | unresolved | note:*)
          status="${status#note:}"
          note="${note#-} (dependabot: ${sver}, ${sorigin})" ;;
        stale)
          if newer "${sver}" "${cand}"; then
            note="update-pins: ${cand}, dependabot: ${sver} (${sorigin})"
            cand="${sver}"
            age='-'
          fi
          newer "${sver}" "${current}" && origin='both' ;;
        *)
          if newer "${sver}" "${current}"; then
            status='stale'
            cand="${sver}"
            age='-'
            note="dependabot ${sorigin}"
            origin='dependabot'
          fi ;;
      esac
    fi
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "${kind}" "${name}" "${loc}" "${current}" "${source}" \
      "${status}" "${cand}" "${age}" "${note:--}" "${locs}" "${origin}"
  done <<< "${resolved}"
}

# ── report ───────────────────────────────────────────────────────────────────

# resolve_all GROUPED: one resolved line per pin:
# kind name loc current source status candidate age note locations (TAB-separated)
function resolve_all() {
  local kind name loc current source locs status cand age note reason
  while IFS=$'\t' read -r kind name loc current source locs; do
    [ -n "${kind}" ] || continue
    if reason="$(held "${name}")"; then
      status='hold'
      cand='-'
      age='-'
      note="${reason}"
    else
      IFS=$'\t' read -r status cand age note < <(resolve "${kind}" "${name}" "${current}" "${source}")
    fi
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "${kind}" "${name}" "${loc}" "${current}" "${source}" \
      "${status}" "${cand}" "${age}" "${note:--}" "${locs}"
  done <<< "${1}"
}

function report() {
  local -r resolved="${1}"
  local kind name loc current source status cand age note locs origin rows='' total=0 statuses=''
  while IFS=$'\t' read -r kind name loc current source status cand age note locs origin; do
    [ -n "${kind}" ] || continue
    statuses+="${status}"$'\n'
    total=$(( total + 1 ))
    rows+="$(printf '%-10s %-44s %-14s %-14s %-5s %-10s %-11s %s  (%s)' "${kind}" "${name:0:44}" "${current:0:14}" \
      "${cand:0:14}" "${age}" "${status}" "${origin}" "${note}" "${loc}")"$'\n'
  done <<< "${resolved}"
  local summary
  summary="$(count_summary "${statuses}" current stale cooldown major unpinned unresolved hold)"
  log "update-pins (discovery) — ${total} pins${summary:+: ${summary}}"
  log "$(printf '%-10s %-44s %-14s %-14s %-5s %-10s %-11s %s' KIND PIN CURRENT CANDIDATE AGE STATUS SOURCE 'NOTE  (LOCATION)')"
  printf "%s" "${rows}" | sort >&5
}

# write_tsv RESOLVED: the resolved pins plus an 11th column, the consequence of
# staying on the pin (for create-release).
function write_tsv() {
  local line
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    printf '%s\t%s\n' "${line}" "$(consequence "${line%%$'\t'*}")"
  done <<< "${1}"
}

# count_summary STATUSES WORD...: "N word, M word" for each WORD present in STATUSES.
function count_summary() {
  local -r statuses="${1}"
  shift
  local summary='' s n
  for s in "$@"; do
    n="$(grep -cx "${s}" <<< "${statuses}" || true)"
    [ "${n}" -gt 0 ] && summary+="${n} ${s}, "
  done
  printf '%s' "${summary%, }"
}

# ── verification ─────────────────────────────────────────────────────────────
# Every stale pin is applied alone in a throwaway git worktree, verified by the
# make targets mapped to its kind, and rolled back; a failure never stops the
# remaining pins. Then every passing pin is applied together for one combined run.
# --apply writes the passing pins to the working tree only after that run passes.

function verify_all() {
  local -r resolved="${1}"
  local stale
  stale="$(awk -F'\t' '$6 == "stale"' <<< "${resolved}")"
  if [ -z "${stale}" ]; then
    log '✅ update-pins (verify) — no stale pins'
    return 0
  fi
  setup_worktree
  local results='' kind name loc current source status cand age note locs origin result logf i=0
  while IFS=$'\t' read -r -u 3 kind name loc current source status cand age note locs origin; do
    i=$(( i + 1 ))
    logf="${LOGDIR}/$(printf '%02d' "${i}")-${kind}-$(printf '%s' "${name}" | tr -c 'A-Za-z0-9._-' '_').log"
    log "🔍 ${kind} ${name} ${current} → ${cand}"
    result="$(verify_pin "${kind}" "${name}" "${current}" "${cand}" "${source}" "${locs}" "${logf}")" \
      || result="$(printf 'fail\tverification error')"
    results+="$(printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s' "${kind}" "${name}" "${current}" "${cand}" "${source}" \
      "${locs}" "${result}" "${logf}")"$'\n'
    rollback_worktree
  done 3<<< "${stale}"
  local combined
  combined="$(combined_run "${results}")"
  verify_report "${results}" "${combined}"
  if [ -n "${APPLY}" ]; then
    apply_passing "${results}" "${combined}"
  fi
  if awk -F'\t' '$7 == "fail" { f = 1 } END { exit !f }' <<< "${results}" \
    || [ "${combined%%$'\t'*}" = 'fail' ]; then
    return 1
  fi
}

# setup_worktree: a detached worktree of HEAD with its submodules checked out
# from the local clones (no network), plus a private tool bin and npm prefix so
# verification never replaces a tool the developer has installed.
function setup_worktree() {
  WT="$(mktemp -d "${TMPDIR:-/tmp}/update-pins.XXXXXX")"
  LOGDIR="${UPDATE_PINS_LOGDIR:-${WT}.logs}"
  TOOLS="${WT}.tools"
  mkdir -p "${LOGDIR}" "${TOOLS}/bin" "${TOOLS}/npm"
  LOGDIR="$(cd "${LOGDIR}" && pwd)"
  rmdir "${WT}"
  git worktree add --quiet --detach "${WT}" HEAD
  trap cleanup_worktree EXIT
  local key path name
  while read -r key path; do
    [ -n "${key}" ] || continue
    name="${key#submodule.}"
    name="${name%.path}"
    git -C "${WT}" -c protocol.file.allow=always -c "submodule.${name}.url=${ROOT}/${path}" \
      submodule update --init --quiet -- "${path}"
  done < <(git config -f .gitmodules --get-regexp '^submodule\..*\.path$' 2>/dev/null || true)
  log "🧪 verifying in ${WT} (logs: ${LOGDIR})"
}

function cleanup_worktree() {
  [ -n "${WT}" ] || return 0
  if [ -f "${ROOT}/${LOCAL_HOOK}" ]; then
    (cd "${ROOT}" && bash "${LOCAL_HOOK}" cleanup "${PLATFORM}") > /dev/null 2>&1 || true
  fi
  git -C "${ROOT}" worktree remove --force "${WT}" > /dev/null 2>&1 || true
  rm -rf "${TOOLS}"
}

function rollback_worktree() {
  git -C "${WT}" checkout --quiet -- .
  git -C "${WT}" clean -fdq
}

# verify_pin KIND NAME CURRENT CAND SOURCE LOCS LOG: "<result>\t<detail>" where
# result is pass | fail | verify-on-ci.
function verify_pin() {
  local -r kind="${1}" name="${2}" current="${3}" cand="${4}" source="${5}" locs="${6}" logf="${7}"
  if ! apply_pin "${WT}" "${kind}" "${name}" "${current}" "${cand}" "${source}" "${locs}" >> "${logf}" 2>&1; then
    printf 'fail\tapply failed\n'
    return
  fi
  local targets
  targets="$(verify_targets "${kind}" "${name}" "${locs}")"
  if [ "${targets}" = 'verify-on-ci' ]; then
    printf 'verify-on-ci\t%s\n' "$(verify_on_ci_reason "${kind}")"
  elif run_targets "${targets}" >> "${logf}" 2>&1; then
    printf 'pass\tmake %s\n' "${targets}"
  else
    printf 'fail\tmake %s\n' "${targets}"
  fi
}

# run_targets TARGETS: make TARGETS (space-separated) in the worktree, with the
# private tool paths.
function run_targets() {
  local targets=()
  read -r -a targets <<< "${1}"
  (cd "${WT}" && GOBIN="${TOOLS}/bin" NPM_CONFIG_PREFIX="${TOOLS}/npm" \
    PATH="${TOOLS}/bin:${TOOLS}/npm/bin:${PATH}" make "${targets[@]}" < /dev/null)
}

# combined_run RESULTS: "<pass|fail|skipped>\t<detail>" for every passing pin
# applied together.
function combined_run() {
  local -r results="${1}"
  local passing kind name current cand source locs result detail logf targets
  passing="$(awk -F'\t' '$7 == "pass"' <<< "${results}")"
  if [ -z "${passing}" ]; then
    printf 'skipped\tno passing pins\n'
    return
  fi
  local -r clog="${LOGDIR}/combined.log"
  while IFS=$'\t' read -r -u 3 kind name current cand source locs result detail logf; do
    apply_pin "${WT}" "${kind}" "${name}" "${current}" "${cand}" "${source}" "${locs}" >> "${clog}" 2>&1 \
      || { printf 'fail\tapply of %s failed (log: %s)\n' "${name}" "${clog}"; rollback_worktree; return; }
  done 3<<< "${passing}"
  targets="$(combined_targets)"
  if run_targets "${targets}" >> "${clog}" 2>&1; then
    printf 'pass\tmake %s (log: %s)\n' "${targets}" "${clog}"
  else
    printf 'fail\tmake %s (log: %s)\n' "${targets}" "${clog}"
  fi
  rollback_worktree
}

# verify_targets KIND NAME LOCS: the make targets that exercise a pin of KIND,
# or "verify-on-ci". The project hook may override (update-pins.local.sh verify).
function verify_targets() {
  local -r kind="${1}" name="${2}" locs="${3}"
  local file="${locs%%,*}" hooked=''
  file="${file%%:*}"
  if [ -f "${LOCAL_HOOK}" ]; then
    hooked="$(bash "${LOCAL_HOOK}" verify "${kind}" "${name}" "${file}" "${PLATFORM}")"
  fi
  if [ -n "${hooked}" ]; then
    printf '%s' "${hooked}"
    return
  fi
  local setup
  setup="$(make_target_for "${file}")"
  case "${kind}" in
    action)
      if has_target lint-workflows; then printf 'lint-workflows'; else printf 'audit'; fi ;;
    gomod)
      printf 'test' ;;
    gotool | npm)
      if [ -n "${setup}" ]; then printf '%s' "${setup}"; else printf 'verify-on-ci'; fi ;;
    tool)
      if [ "${PLATFORM}" = 'Linux' ] && [ -n "${setup}" ]; then printf '%s' "${setup}"; else printf 'verify-on-ci'; fi ;;
    toolchain)
      if [ "${PLATFORM}" = 'Linux' ]; then printf 'test'; else printf 'verify-on-ci'; fi ;;
    *)
      printf 'verify-on-ci' ;;
  esac
}

function verify_on_ci_reason() {
  case "${1}" in
    tool) printf 'release binaries install only on Linux (macOS uses Homebrew)' ;;
    toolchain) printf 'the toolchain switch is exercised on the CI runner' ;;
    image) printf 'image builds run in the CI stacks' ;;
    node) printf 'Node runs in CI only' ;;
    *) printf 'no local verification for this kind' ;;
  esac
}

# combined_targets: the project's combined-run targets (update-pins.local.sh
# combined), else lint, audit, test.
function combined_targets() {
  local hooked=''
  if [ -f "${LOCAL_HOOK}" ]; then
    hooked="$(bash "${LOCAL_HOOK}" combined "${PLATFORM}")"
  fi
  printf '%s' "${hooked:-lint audit test}"
}

function has_target() {
  make -n "${1}" > /dev/null 2>&1
}

# make_target_for FILE: the make target whose recipe runs FILE.
function make_target_for() {
  local -r file="${1}"
  local mk
  for mk in Makefile .standards/templates/Makefile.canonical templates/Makefile.canonical; do
    [ -f "${mk}" ] || continue
    awk -v f="${file}" '/^[A-Za-z0-9_.-]+:/ { t = $1; sub(/:.*/, "", t) } /^\t/ && index($0, f) { print t; exit }' "${mk}"
  done | head -1
}

# ── apply ────────────────────────────────────────────────────────────────────

# apply_pin ROOT KIND NAME CURRENT CAND SOURCE LOCS: write CAND into the tree at
# ROOT. The project hook may handle a pin itself (update-pins.local.sh apply;
# exit 0 handled, 3 not handled).
function apply_pin() {
  local -r root="${1}" kind="${2}" name="${3}" current="${4}" cand="${5}" source="${6}" locs="${7}"
  if [ -f "${LOCAL_HOOK}" ]; then
    local rc=0
    bash "${LOCAL_HOOK}" apply "${root}" "${kind}" "${name}" "${current}" "${cand}" "${locs}" || rc=$?
    [ "${rc}" -eq 3 ] || return "${rc}"
  fi
  local new
  new="$(same_style "${cand}" "${current}")"
  case "${kind}" in
    action) apply_action "${root}" "${name}" "${current}" "${cand}" "${source#github:}" ;;
    image) replace_all "${root}" "${name}@${current}" "${name}@${cand}" ;;
    tool) replace_at "${root}" "${locs}" "${current}" "${new}" && refresh_checksums "${root}" "${locs}" "${source#github:}" "${current}" "${cand}" ;;
    gotool | toolchain | npm | node) replace_at "${root}" "${locs}" "${current}" "${new}" ;;
    gomod) apply_gomod "${root}" "${name}" "${cand}" "${locs}" ;;
    *) printf 'no apply adapter for %s\n' "${kind}"; return 1 ;;
  esac
}

# same_style CAND CURRENT: CAND with a leading "v" exactly when CURRENT has one.
function same_style() {
  local -r cand="${1}" current="${2}"
  if [[ "${current}" == v* || "${current}" == go* ]]; then
    printf '%s' "${cand}"
  else
    printf '%s' "${cand#v}"
  fi
}

# replace_at ROOT LOCS OLD NEW: OLD → NEW on each listed file:line.
function replace_at() {
  local -r root="${1}" locs="${2}" old="${3}" new="${4}"
  local loc file line all=()
  IFS=',' read -r -a all <<< "${locs}"
  for loc in "${all[@]}"; do
    file="${loc%%:*}"
    line="${loc##*:}"
    OLD="${old}" NEW="${new}" LINE="${line}" perl -pi -e 'if ($. == $ENV{LINE}) { s/\Q$ENV{OLD}\E/$ENV{NEW}/ }' \
      "${root}/${file}"
    sed -n "${line}p" "${root}/${file}" | grep -qF -- "${new}" \
      || { printf 'replace failed at %s: %s not found\n' "${loc}" "${old}"; return 1; }
    # A "Pinned version: X" header comment restates the pin; keep it in step.
    OLD="${old}" NEW="${new}" perl -pi -e 's/^(#\s*Pinned version:\s*)\Q$ENV{OLD}\E\s*$/$1$ENV{NEW}\n/' "${root}/${file}"
  done
}

# replace_all ROOT OLD NEW: OLD → NEW in every repo-owned file containing OLD.
function replace_all() {
  local -r root="${1}" old="${2}" new="${3}"
  local files
  files="$(cd "${root}" && source_files | xargs grep -lF -- "${old}" 2>/dev/null || true)"
  [ -n "${files}" ] || { printf 'nothing to replace: %s\n' "${old}"; return 1; }
  while IFS= read -r f; do
    OLD="${old}" NEW="${new}" perl -pi -e 's/\Q$ENV{OLD}\E/$ENV{NEW}/g' "${root}/${f}"
  done <<< "${files}"
}

# apply_action ROOT REF CURRENT CAND REPO: the commit SHA of tag CAND, with the
# exact version comment, everywhere REF is pinned at CURRENT.
function apply_action() {
  local -r root="${1}" ref="${2}" current="${3}" cand="${4}" repo="${5}"
  local sha files
  sha="$(fetch "https://api.github.com/repos/${repo}/commits/${cand}" | jq -r '.sha // empty')"
  [[ "${sha}" =~ ^[0-9a-f]{40}$ ]] || { printf 'no commit for %s@%s\n' "${repo}" "${cand}"; return 1; }
  files="$(cd "${root}" && git grep -lF -- "${ref}@" -- '.github' || true)"
  [ -n "${files}" ] || { printf 'no workflow uses %s\n' "${ref}"; return 1; }
  while IFS= read -r f; do
    REF="${ref}" CUR="${current}" SHA="${sha}" CAND="${cand}" perl -pi -e \
      's/(uses:\s*\Q$ENV{REF}\E@)[0-9a-f]{40}(\s*#\s*)\Q$ENV{CUR}\E(?![0-9A-Za-z.])/$1$ENV{SHA}$2$ENV{CAND}/g' "${root}/${f}"
  done <<< "${files}"
  (cd "${root}" && git grep -qF -- "${ref}@${sha}" -- '.github') \
    || { printf 'no %s pin at %s was rewritten\n' "${ref}" "${current}"; return 1; }
}

# refresh_checksums ROOT LOCS REPO CURRENT CAND: every committed SHA-256 in the
# pin's files is mapped to the release asset it verifies (by the digest GitHub
# publishes for each asset), and replaced by the digest of the same asset in the
# CAND release. No binary is downloaded.
function refresh_checksums() {
  local -r root="${1}" locs="${2}" repo="${3}" current="${4}" cand="${5}"
  local old_assets new_assets loc file hex asset new_asset new_hex all=()
  old_assets="$(release_assets "${repo}" "${current}")" || { printf 'no release %s for %s\n' "${current}" "${repo}"; return 1; }
  new_assets="$(release_assets "${repo}" "${cand}")" || { printf 'no release %s for %s\n' "${cand}" "${repo}"; return 1; }
  IFS=',' read -r -a all <<< "${locs}"
  for loc in "${all[@]}"; do
    file="${loc%%:*}"
    while IFS= read -r hex; do
      [ -n "${hex}" ] || continue
      asset="$(awk -F'\t' -v h="sha256:${hex}" '$2 == h { print $1; exit }' <<< "${old_assets}")"
      [ -n "${asset}" ] || { printf '%s: %s matches no asset of %s %s\n' "${file}" "${hex}" "${repo}" "${current}"; return 1; }
      new_asset="${asset//${current#v}/${cand#v}}"
      new_hex="$(awk -F'\t' -v a="${new_asset}" '$1 == a { sub(/^sha256:/, "", $2); print $2; exit }' <<< "${new_assets}")"
      [[ "${new_hex}" =~ ^[0-9a-f]{64}$ ]] \
        || { printf '%s: no published digest for %s in %s %s\n' "${file}" "${new_asset}" "${repo}" "${cand}"; return 1; }
      OLD="${hex}" NEW="${new_hex}" perl -pi -e 's/\Q$ENV{OLD}\E/$ENV{NEW}/g' "${root}/${file}"
      printf '%s: %s → %s (%s)\n' "${file}" "${asset}" "${new_asset}" "${new_hex}"
    done < <(grep -oE '[A-Z0-9_]*SHA256[A-Z0-9_]*=["'"'"']?[0-9a-f]{64}' "${root}/${file}" | grep -oE '[0-9a-f]{64}$' | sort -u)
  done
}

# release_assets REPO VERSION: "<asset name>\t<digest>" for the release tagged
# VERSION (tried as given, then with and without a leading v).
function release_assets() {
  local -r repo="${1}" version="${2}"
  local tag body
  for tag in "${version}" "v${version#v}" "${version#v}"; do
    body="$(fetch "https://api.github.com/repos/${repo}/releases/tags/${tag}")" || continue
    jq -r '.assets[] | "\(.name)\t\(.digest // "")"' <<< "${body}"
    return 0
  done
  return 1
}

# apply_gomod ROOT MODULE CAND LOCS: go get + tidy (+ vendor when the module vendors).
function apply_gomod() {
  local -r root="${1}" module="${2}" cand="${3}" locs="${4}"
  local dir
  dir="$(dirname "${root}/${locs%%:*}")"
  (cd "${dir}" && GOFLAGS=-mod=mod go get "${module}@${cand}" && go mod tidy \
    && { [ ! -d vendor ] || go mod vendor; })
}

# apply_passing RESULTS COMBINED: write every passing pin to the working tree,
# only when the combined run passed.
function apply_passing() {
  local -r results="${1}" combined="${2}"
  if [ "${combined%%$'\t'*}" != 'pass' ]; then
    log '⛔ --apply: nothing written — the combined run did not pass'
    return
  fi
  local kind name current cand source locs result detail logf
  while IFS=$'\t' read -r -u 3 kind name current cand source locs result detail logf; do
    [ "${result}" = 'pass' ] || continue
    if apply_pin "${ROOT}" "${kind}" "${name}" "${current}" "${cand}" "${source}" "${locs}" >> "${LOGDIR}/apply.log" 2>&1; then
      log "✍️  applied ${kind} ${name} ${current} → ${cand}"
    else
      log "❌ apply failed: ${kind} ${name} (log: ${LOGDIR}/apply.log)"
    fi
  done 3<<< "${results}"
  log '   review with git diff, then stage'
}

# ── verification report ──────────────────────────────────────────────────────

function verify_report() {
  local -r results="${1}" combined="${2}"
  local kind name current cand source locs result detail logf rows='' kinds=''
  while IFS=$'\t' read -r kind name current cand source locs result detail logf; do
    [ -n "${kind}" ] || continue
    rows+="$(printf '%-10s %-40s %-14s %-14s %-12s %s' "${kind}" "${name:0:40}" "${current:0:14}" "${cand:0:14}" \
      "${result}" "${detail}$([ "${result}" = 'fail' ] && printf '  (log: %s)' "${logf}")")"$'\n'
    kinds+="${kind}"$'\n'
  done <<< "${results}"
  log ''
  log "update-pins (verify) — $(count_summary "$(cut -f7 <<< "${results}")" pass fail verify-on-ci); combined: ${combined%%$'\t'*} — ${combined#*$'\t'}"
  log "$(printf '%-10s %-40s %-14s %-14s %-12s %s' KIND PIN CURRENT CANDIDATE RESULT DETAIL)"
  printf '%s' "${rows}" | sort >&5
  log ''
  log 'Consequences of staying on a stale pin:'
  local k
  while IFS= read -r k; do
    [ -n "${k}" ] && log "  ${k}: $(consequence "${k}")"
  done < <(sort -u <<< "${kinds}")
}

function consequence() {
  case "${1}" in
    action) printf 'missed security fixes in the action; deprecated Node runtimes eventually stop running on GitHub-hosted runners' ;;
    image) printf 'unpatched OS packages and CA bundle in the build stage; CVEs in the toolchain image that builds release binaries' ;;
    tool) printf 'scanners miss detections a current release flags; signing/attestation formats drift from what verifiers expect' ;;
    gotool) printf 'missing analyzer rules (gosec) or vulnerability-database support (govulncheck)' ;;
    toolchain) printf 'unpatched standard-library CVEs (crypto/tls, net/http) compiled into every release binary' ;;
    gomod) printf 'known CVEs in linked dependencies (govulncheck reports the reachable ones)' ;;
    npm) printf 'trusted publishing (OIDC) breaks when the registry raises its minimum client version' ;;
    node) printf 'an end-of-life runtime without security updates' ;;
    *) printf 'unknown' ;;
  esac
}

main "${@:-}"
