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
#   --pins-md FILE   also write every pin as a Markdown table to FILE (docs/current_pins.md)
#                    (--apply rewrites docs/current_pins.md after writing the pins, when it exists)
#   --check-pins-doc FILE
#                    local only (no network): the Pinned column of FILE matches every pin
#                    discovered in the tree; exit 1 listing each difference (the pre-commit check)
#   --from-pins-doc FILE
#                    local only (no network): report the pins and statuses FILE records, after the
#                    --check-pins-doc check (the create-release gate); no FILE is fine for a tree
#                    with no pins
# Exit status: 1 when a pin or the combined run failed, else 0.
#
# Pin kinds:
#   action     workflow `uses: owner/repo@<sha>  # vX.Y.Z`
#   image      `name:tag@sha256:<digest>` in any repo-owned file
#   tool       `X_VERSION=` in a file that downloads a GitHub release asset, or
#              clones its release tag (`*_COMMIT=` is the verified commit)
#   gotool     `go install module@version` (TOOL_MODULE/TOOL_VERSION, X_MODULE='m@v')
#   toolchain  go.mod `toolchain goX.Y.Z`
#   gomod      go.mod direct requirements (go.sum + vendor/ follow them)
#   npm        `npm install <pkg>@<version>` (literal or X_VERSION variable)
#   node       .nvmrc
#   python     .python-version (CPython builds published for setup-python)
#   pypi       `name==version` in a hash-locked requirements file, or an exact
#              pyproject build-system requirement
#   npmlock    exact dependencies of a package.json beside its package-lock.json
#   runner     workflow `runs-on:` / matrix `os:` image labels (newer images report-only)
#   devimage   containers/NAME/image.ref: the pinned build context vs the tree's (a hold until the
#              new context is published; apply pins it by digest)
#   unpinned   inputs that float: @latest, apk/apt packages without a version,
#              Homebrew installs, FROM / image: references without a digest,
#              `*-latest` runners, npx and pip installs without a version or
#              hashes, package.json version ranges
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
# Usage: update-pins.sh [--apply | --discover-only | --from-pins-doc FILE] [--tsv FILE] [--report FILE]

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
HOLDS=''
ROOT=''
MODE='verify'
APPLY=''
TSV_OUT=''
PINS_MD=''
CHECK_DOC=''
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
  if [ "${MODE}" = 'check-doc' ]; then
    load_hook
    check_pins_doc "${CHECK_DOC}"
    return
  fi
  if [ "${MODE}" = 'from-doc' ]; then
    load_hook
    report_from_pins_doc "${CHECK_DOC}"
    return
  fi
  cache_github_token
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
  [ -z "${PINS_MD}" ] || write_pins_md "${resolved}" > "${PINS_MD}"
  [ "${MODE}" = 'discover' ] && return 0
  local rc=0
  verify_all "${resolved}" || rc=$?
  # The pins table follows the pins it describes: rewritten from the tree --apply just wrote.
  if [ -n "${APPLY}" ] && [ -f docs/current_pins.md ]; then
    bash "${BASH_SOURCE[0]}" --discover-only --pins-md docs/current_pins.md > /dev/null
    log '📝 docs/current_pins.md rewritten'
  fi
  return "${rc}"
}

# report_from_pins_doc FILE: the discovery summary and --tsv file, from what FILE records.
function report_from_pins_doc() {
  local -r doc="${1}"
  local rows=''
  if [ -f "${doc}" ] || [ -n "$(discover)" ]; then
    check_pins_doc "${doc}"
    rows="$(pins_doc_rows "${doc}")"
  fi
  local statuses total summary
  statuses="$(cut -f6 <<< "${rows}")"
  total="$(grep -c . <<< "${rows}" || true)"
  summary="$(count_summary "${statuses}" current stale cooldown major unpinned unresolved hold)"
  log "update-pins (discovery) — ${total} pins${summary:+: ${summary}}"
  [ -z "${TSV_OUT}" ] || write_tsv "${rows}" > "${TSV_OUT}"
}

# pins_doc_rows FILE: the table write_pins_md wrote, as resolved rows (kind, name, location,
# pinned, source, status, candidate).
function pins_doc_rows() {
  awk -F' \\| ' -v OFS='\t' '/^\| [a-z]+ \| `/ {
    for (i = 1; i <= NF; i++) gsub(/`|^\| | \|$/, "", $i)
    print $1, $2, $6, $3, "-", $4, $5
  }' "${1}"
}

# check_pins_doc FILE: every pin discovered in the tree (kind, name, pinned value) against the
# Kind / Pin / Pinned columns of FILE; no network. Exit 1 listing each difference.
function check_pins_doc() {
  local -r doc="${1}"
  [ -f "${doc}" ] || { log "❌ ${doc} not found (make pins-doc)"; exit 1; }
  local want have diff
  want="$(group "$(discover)" | awk -F'\t' -v OFS='\t' '{ print $1, $2, $4 }' | sort -u)"
  have="$(awk -F' \\| ' '/^\| [a-z]+ \| `/ { k = $1; sub(/^\| /, "", k); n = $2; p = $3; gsub(/`/, "", n); gsub(/`/, "", p); print k "\t" n "\t" p }' "${doc}" | sort -u)"
  diff="$(diff <(printf '%s\n' "${have}") <(printf '%s\n' "${want}") | grep -E '^[<>] ' || true)"
  if [ -n "${diff}" ]; then
    log "❌ ${doc} does not match the pins in the tree (make pins-doc, then commit it with the pins):"
    printf '%s\n' "${diff}" | sed -e "s|^< |  only in ${doc}: |" -e 's|^> |  only in the tree: |' >&5
    exit 1
  fi
  log "✅ ${doc} matches the pins in the tree"
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
      --pins-md)
        [ -n "${2:-}" ] || { log '❌ Error: --pins-md needs a file'; exit 1; }
        PINS_MD="${2}"
        shift ;;
      --check-pins-doc)
        [ -n "${2:-}" ] || { log '❌ Error: --check-pins-doc needs a file'; exit 1; }
        CHECK_DOC="${2}"
        MODE='check-doc'
        shift ;;
      --from-pins-doc)
        [ -n "${2:-}" ] || { log '❌ Error: --from-pins-doc needs a file'; exit 1; }
        CHECK_DOC="${2}"
        MODE='from-doc'
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
  discover_runners
  discover_python
  discover_pypi
  discover_npmlock
  discover_devimages
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

# files_of GLOB...: repo-owned files, minus this driver and the project hook (both
# quote the patterns they look for and rewrite).
function files_of() {
  source_files "$@" | grep_exclude -vxF -e 'scripts/release/update-pins.sh' -e "${LOCAL_HOOK}"
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
    # A release tag cloned with git (`git clone --branch v${X_VERSION} https://github.com/O/R.git`).
    [ -n "${repo}" ] || repo="$(grep -oE 'github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_-]+\.git' "${file}" | head -1 \
      | cut -d/ -f2-3 | sed 's/\.git$//')"
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

# Tool images (containers/NAME/image.ref, scripts/lib/dev-image.sh): the pinned build context
# against the tree's. A changed context (a tool pin bumped in a Containerfile, a new base) is a
# hold until the image is published (dev-image.yml, after the commit), then stale until pinned.
function discover_devimages() {
  [ -f scripts/lib/dev-image.sh ] || return 0
  local cf name ref pinned tree
  for cf in containers/*/Containerfile; do
    [ -f "${cf}" ] || continue
    name="$(basename "$(dirname "${cf}")")"
    ref="containers/${name}/image.ref"
    [ -s "${ref}" ] || continue
    pinned="$(sed -nE 's/.*:(ctx-[0-9a-f]+)@sha256:.*/\1/p' "${ref}" | head -1)"
    tree="ctx-$(devimage_context "${name}")" || continue
    record devimage "${name}" "${ref}:1" "${pinned:-none}" "${tree}"
  done
}

# devimage_context NAME: the tree's build-context hash for containers/NAME (its .standards base as
# pinned there, for an image that builds on it).
function devimage_context() {
  (
    # shellcheck source=../lib/dev-image.sh
    source scripts/lib/dev-image.sh
    local base=''
    if dev_has_base . "${1}"; then
      base="$(dev_pinned_ref .standards dev "$(dev_context_hash .standards dev)")" || base=''
    fi
    dev_context_hash . "${1}" "${base}"
  )
}

# devimage_repository NAME: where containers/NAME is published.
function devimage_repository() {
  (
    # shellcheck source=../lib/dev-image.sh
    source scripts/lib/dev-image.sh
    dev_repository . "${1}"
  )
}

# Workflow runner labels (`runs-on:` or a matrix `os:`) that name an exact image.
function discover_runners() {
  local line label
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    label="$(sed -E 's/.*(runs-on|os):[[:space:]]*["'\'']?([a-z]+-[0-9][A-Za-z0-9.-]*).*/\2/' <<< "${line#*:*:}")"
    record runner "${label}" "$(cut -d: -f1-2 <<< "${line}")" "${label}" 'runner-images'
  done < <(files_of '.github/workflows/*.yml' '.github/workflows/*.yaml' \
    | grep_files '^[[:space:]]*(-[[:space:]]+)?(runs-on|os):[[:space:]]*["'\'']?(ubuntu|macos|windows)-[0-9]')
}

# .python-version: the exact CPython release setup-python installs.
function discover_python() {
  local f
  while IFS= read -r f; do
    [ -n "${f}" ] || continue
    record python python "${f}:1" "$(tr -d '[:space:]' < "${f}")" 'python-versions'
  done < <(files_of '.python-version' '*/.python-version')
}

# Hash-locked pip requirements (`name==version` + --hash lines) and exact
# pyproject build-system requirements.
function discover_pypi() {
  local line spec
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    spec="$(sed -E 's/^[^:]+:[0-9]+:[[:space:]]*([A-Za-z0-9._-]+==[^[:space:]\\]+).*/\1/' <<< "${line}")"
    record pypi "${spec%%==*}" "$(cut -d: -f1-2 <<< "${line}")" "${spec##*==}" "pypi:${spec%%==*}"
  done < <(files_of '*requirements*.txt' | grep_files '^[A-Za-z0-9._-]+==[0-9]')
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    while IFS= read -r spec; do
      [ -n "${spec}" ] || continue
      record pypi "${spec%%==*}" "$(cut -d: -f1-2 <<< "${line}")" "${spec##*==}" "pypi:${spec%%==*}"
    done < <(grep -oE '"[A-Za-z0-9._-]+==[0-9][^"]*"' <<< "${line#*:*:}" | tr -d '"')
  done < <(files_of 'pyproject.toml' '*/pyproject.toml' | grep_files '^requires[[:space:]]*=')
}

# Exact dependency versions in a package.json that sits next to its
# package-lock.json (the lockfile pins the rest of the tree).
function discover_npmlock() {
  local pkg dir
  while IFS= read -r pkg; do
    [ -n "${pkg}" ] || continue
    dir="$(dirname "${pkg}")"
    [ -f "${dir}/package-lock.json" ] || continue
    awk -v f="${pkg}" '
      /"(dependencies|devDependencies)"[[:space:]]*:/ { blk = 1; next }
      blk && /}/ { blk = 0; next }
      blk && match($0, /"[^"]+"[[:space:]]*:[[:space:]]*"[0-9][0-9.]*"/) {
        split(substr($0, RSTART, RLENGTH), kv, "\"")
        printf "npmlock\t%s\t%s:%d\t%s\tnpm:%s\n", kv[2], f, NR, kv[4], kv[2]
      }' "${pkg}"
  done < <(files_of 'package.json' '*/package.json')
}

# digest_pinned_filter: drop file:line:text matches from a Containerfile whose image is
# pinned by digest (containers/NAME/image.ref) — packages it installs at build time are
# fixed by that digest.
function digest_pinned_filter() {
  local line file
  while IFS= read -r line; do
    file="${line%%:*}"
    [[ "${file}" == */Containerfile ]] && [ -s "$(dirname "${file}")/image.ref" ] && continue
    printf '%s\n' "${line}"
  done
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
  done < <(files_of '*Containerfile*' '*Dockerfile*' '*.sh' | grep_files 'apk add' | grep -vE 'apk add[^#]*=[0-9]' | digest_pinned_filter)
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    record unpinned "apt:$(sed -E 's/.*apt(-get)? install[[:space:]]+(-[a-zA-Z-]+[[:space:]]+)*//; s/[[:space:]]+--.*//' <<< "${line}" | tr -s ' ' ',')" \
      "$(cut -d: -f1-2 <<< "${line}")" 'no version' 'floats'
  done < <(files_of '*.sh' '*Containerfile*' '*Dockerfile*' | grep_files 'apt(-get)? install' | grep -v "log '" | grep -vE 'install[^#]*=[0-9]' \
    | digest_pinned_filter)
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
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    record unpinned "$(grep -oE '(ubuntu|macos|windows)-latest' <<< "${line#*:*:}" | head -1)" "$(cut -d: -f1-2 <<< "${line}")" \
      'latest' 'floats (runner image)'
  done < <(files_of '.github/workflows/*.yml' '.github/workflows/*.yaml' | grep_files '(ubuntu|macos|windows)-latest')
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    record unpinned "npx:$(sed -E 's/.*npx[[:space:]]+(-[-a-z]+[[:space:]]+)*([^[:space:]"]+).*/\2/' <<< "${line#*:*:}")" \
      "$(cut -d: -f1-2 <<< "${line}")" 'no version' 'floats'
  done < <(files_of '*.sh' 'Makefile' '*.mk' '*.yml' '*.yaml' | grep_files 'npx[[:space:]]' | grep -v "log '" \
    | grep -vE 'npx[[:space:]]+(-[-a-z]+[[:space:]]+)*[@a-z0-9][^[:space:]]*@[0-9]')
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    record unpinned "pip:$(sed -E 's/.*pip[0-9]? install[[:space:]]+(-[-a-zA-Z]+[[:space:]]+)*//' <<< "${line#*:*:}" | xargs | tr ' ' ',')" \
      "$(cut -d: -f1-2 <<< "${line}")" 'no version' 'floats'
  done < <(files_of '*.sh' 'Makefile' '*.mk' '*.yml' '*.yaml' '*Containerfile*' '*Dockerfile*' \
    | grep_files '(pip[0-9]?|pipx) install' | grep -v "log '" | grep -v -- '--require-hashes')
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    record unpinned "npm:$(sed -E 's/^[^:]+:[0-9]+:[[:space:]]*"([^"]+)".*/\1/' <<< "${line}")" "$(cut -d: -f1-2 <<< "${line}")" \
      'range' 'floats'
  done < <(files_of 'package.json' '*/package.json' | grep_files '^[[:space:]]*"[^"]+"[[:space:]]*:[[:space:]]*"[~^>*]')
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
    curl -fsL --retry 3 --retry-all-errors --retry-delay 2 --max-time 60 -H "Authorization: Bearer $(github_token)" \
      -H 'Accept: application/vnd.github+json' "${url}"
  else
    curl -fsL --retry 3 --retry-all-errors --retry-delay 2 --max-time 60 "${url}"
  fi
}

# github_token: the token resolved once by cache_github_token (every fetch runs in a subshell, so
# a lazy cache would be lost and `gh auth token`, a keychain read, would run per request).
function github_token() {
  printf '%s' "${GITHUB_TOKEN:-}"
}

# cache_github_token: GITHUB_TOKEN, else `gh auth token` read once, exported for every
# subshell and child (verification worktrees, the project hook).
function cache_github_token() {
  [ -n "${UPDATE_PINS_FIXTURES:-}" ] && return 0
  if [ -z "${GITHUB_TOKEN:-}" ]; then
    GITHUB_TOKEN="$(gh auth token 2>/dev/null || true)"
  fi
  export GITHUB_TOKEN
}

# github_releases O/R [COUNT]: the repository's latest COUNT releases (default 20; REST: 100)
# as [{tag_name, published_at, draft, prerelease}]. GraphQL returns only those fields — the REST list carries every asset, which
# runs to megabytes for a project like sigstore/cosign — so it needs a token; without one
# (and in tests) the REST list is used.
function github_releases() {
  local -r repo="${1}" count="${2:-20}"
  if [ -n "${UPDATE_PINS_FIXTURES:-}" ] || [ -z "$(github_token)" ]; then
    fetch "https://api.github.com/repos/${repo}/releases?per_page=100"
    return
  fi
  local query body
  query="$(jq -n --arg o "${repo%%/*}" --arg n "${repo#*/}" --argjson c "${count}" '{
    query: "query($o: String!, $n: String!, $c: Int!) { repository(owner: $o, name: $n) { releases(first: $c, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { tagName publishedAt isDraft isPrerelease } } } }",
    variables: {o: $o, n: $n, c: $c}}')"
  body="$(curl -fsSL --retry 3 --retry-all-errors --retry-delay 2 --max-time 60 -X POST \
    -H "Authorization: Bearer $(github_token)" -d "${query}" https://api.github.com/graphql)" || return 1
  jq -e '[.data.repository.releases.nodes[] | {tag_name: .tagName, published_at: .publishedAt,
           draft: .isDraft, prerelease: .isPrerelease}]' <<< "${body}"
}

# versions_github O/R: "<tag>\t<epoch>" for every published, non-draft release.
function versions_github() {
  local body
  body="$(github_releases "${1}")" || { failed_lookup; return 0; }
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
  if [ -z "${UPDATE_PINS_FIXTURES:-}" ]; then
    latest_first go "${root}" "${current}"
    return
  fi
  esc="$(goproxy_escape "${root}")"
  while IFS= read -r v; do
    [ -n "${v}" ] || continue
    [[ "${v}" =~ ${SEMVER_RE} ]] || continue
    newer "${v}" "${current}" || continue
    t="$(fetch "https://proxy.golang.org/${esc}/@v/${v}.info" | jq -r '.Time | sub("\\.[0-9]+"; "") | fromdateiso8601' 2>/dev/null || true)"
    if [ -n "${t}" ]; then printf '%s\t%s\n' "${v}" "${t}"; else failed_lookup; fi
  done < <(fetch "https://proxy.golang.org/${esc}/@v/list" | sed 's/+incompatible$//')
}

# versions_toolchain CURRENT: "<goX.Y.Z>\t<epoch>" for Go releases newer than CURRENT. Each
# release's .info (about 200 bytes) is probed in order — the next patches of CURRENT's minor, then
# each next minor from .0 — until one does not exist; the full toolchain list (every platform of
# every release) is read only in tests.
function versions_toolchain() {
  local -r current="${1}"
  if [ -n "${UPDATE_PINS_FIXTURES:-}" ]; then
    versions_toolchain_list "${current}"
    return
  fi
  local -r cur="${current#go}"
  local major minor patch m p t
  IFS=. read -r major minor patch <<< "${cur}"
  for (( m = minor; m <= minor + 2; m++ )); do
    p=0
    [ "${m}" -eq "${minor}" ] && p=$(( ${patch:-0} + 1 ))
    while t="$(toolchain_time "go${major}.${m}.${p}")"; do
      printf 'go%s.%s.%s\t%s\n' "${major}" "${m}" "${p}" "${t}"
      p=$(( p + 1 ))
    done
    # A minor with no .0 release does not exist yet; later ones cannot either.
    [ "${m}" -eq "${minor}" ] || [ "${p}" -gt 0 ] || break
  done
}

# toolchain_time goX.Y.Z: the release's publish time (epoch); fails when it does not exist.
function toolchain_time() {
  local t
  t="$(fetch "https://proxy.golang.org/golang.org/toolchain/@v/v0.0.1-${1}.linux-amd64.info" 2> /dev/null \
    | jq -r '.Time | sub("\\.[0-9]+"; "") | fromdateiso8601' 2> /dev/null)" || return 1
  [ -n "${t}" ] && printf '%s' "${t}"
}

# versions_toolchain_list CURRENT: as versions_toolchain, from the proxy's full toolchain list.
function versions_toolchain_list() {
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

# latest_first SYSTEM NAME CURRENT: "<version>\t<epoch>" for an npm, PyPI or Go package from
# metadata only, smallest first. The latest stable version and its publish date settle almost
# every pin (current, or an eligible candidate in the same major); the recent version list is
# read only when the latest is inside the cooldown or a new major — then an older release may be
# the newest eligible one.
function latest_first() {
  local -r system="${1}" name="${2}" current="${3}"
  local line v t
  line="$(latest_version "${system}" "${name}")" || { failed_lookup; return 0; }
  IFS=$'\t' read -r v t <<< "${line}"
  printf '%s\n' "${line}"
  if [ "${v#v}" = "${current#v}" ] || ! newer "${v}" "${current}"; then
    return 0
  fi
  if [ $(( (NOW - t) / DAY )) -ge "${COOLDOWN_DAYS}" ] && [ "$(major_of "${v}")" = "$(major_of "${current}")" ]; then
    return 0
  fi
  recent_versions "${system}" "${name}"
}

# latest_version SYSTEM NAME: "<version>\t<epoch>" of the latest stable release.
function latest_version() {
  local -r system="${1}" name="${2}"
  local enc v body
  case "${system}" in
    npm)
      v="$(fetch "https://registry.npmjs.org/-/package/${name/\//%2f}/dist-tags" | jq -r '.latest // empty')" || return 1
      [ -n "${v}" ] || return 1
      enc="$(jq -rn --arg n "${name}" '$n | @uri')"
      fetch "https://api.deps.dev/v3/systems/npm/packages/${enc}/versions/${v}" \
        | jq -r '"\(.versionKey.version)\t\(.publishedAt | sub("\\.[0-9]+"; "") | fromdateiso8601)"' ;;
    pypi)
      recent_versions pypi "${name}" | awk -F'\t' '$1 ~ /^[0-9]+(\.[0-9]+)*$/' | head -1 ;;
    go)
      body="$(fetch "https://proxy.golang.org/$(goproxy_escape "${name}")/@latest")" || return 1
      jq -r '"\(.Version | sub("\\+incompatible$"; ""))\t\(.Time | sub("\\.[0-9]+"; "") | fromdateiso8601)"' <<< "${body}" ;;
  esac
}

# recent_versions SYSTEM NAME: "<version>\t<epoch>" for recent releases: PyPI's releases feed
# (the last 40), else deps.dev's version list (versions and dates only).
function recent_versions() {
  local -r system="${1}" name="${2}"
  if [ "${system}" = 'pypi' ]; then
    fetch "https://pypi.org/rss/project/${name}/releases.xml" | awk '
      BEGIN { split("Jan Feb Mar Apr May Jun Jul Aug Sep Oct Nov Dec", m, " "); for (i = 1; i <= 12; i++) mon[m[i]] = sprintf("%02d", i) }
      /<item>/ { v = "" }
      match($0, /<title>[^<]+<\/title>/) { v = substr($0, RSTART + 7, RLENGTH - 15) }
      match($0, /<pubDate>[^<]+<\/pubDate>/) {
        split(substr($0, RSTART + 9, RLENGTH - 19), d, " ")
        if (v != "") printf "%s\t%s-%s-%sT%sZ\n", v, d[4], mon[d[3]], d[2], d[5]
      }' | while IFS=$'\t' read -r v t; do
        printf '%s\t%s\n' "${v}" "$(jq -rn --arg t "${t}" '$t | fromdateiso8601')"
      done
    return
  fi
  versions_depsdev "${system}" "${name}" | sed 's/+incompatible\t/\t/'
}

# versions_depsdev SYSTEM NAME: "<version>\t<epoch>" for every version deps.dev lists for an npm,
# PyPI or Go package — one small response with versions and publish dates only (the registries'
# own documents carry every version's full metadata or files: tens of MB for npm itself).
# Tests read the registries' fixtures instead (UPDATE_PINS_FIXTURES).
function versions_depsdev() {
  local -r system="${1}" name="${2}"
  local enc body
  enc="$(jq -rn --arg n "${name}" '$n | @uri')"
  body="$(fetch "https://api.deps.dev/v3/systems/${system}/packages/${enc}")" || { failed_lookup; return 0; }
  jq -r '.versions[] | select(.publishedAt != null)
         | "\(.versionKey.version)\t\(.publishedAt | sub("\\.[0-9]+"; "") | fromdateiso8601)"' <<< "${body}" 2>/dev/null \
    || failed_lookup
}

function versions_npm() {
  local body
  if [ -z "${UPDATE_PINS_FIXTURES:-}" ]; then latest_first npm "${1}" "${2:-}"; return; fi
  body="$(fetch "https://registry.npmjs.org/${1}")" || { failed_lookup; return 0; }
  jq -r '.time | to_entries[] | select(.key | test("^[0-9]+\\.[0-9]+\\.[0-9]+$"))
         | "\(.key)\t\(.value | sub("\\.[0-9]+"; "") | fromdateiso8601)"' <<< "${body}" 2>/dev/null \
    || failed_lookup
}

# versions_node MAJOR: "<vX.Y.Z>\t<epoch>" for releases in that (LTS) major: nodejs/node's 50 newest
# GitHub releases (tags and dates only); the full dist index (every file of every release) only in
# tests.
function versions_node() {
  local body
  if [ -z "${UPDATE_PINS_FIXTURES:-}" ]; then
    body="$(github_releases nodejs/node 50)" || { failed_lookup; return 0; }
    jq -r --arg m "v${1%%.*}." '.[] | select(.draft | not) | select(.prerelease | not)
           | select(.tag_name | startswith($m)) | select(.tag_name | test("^v[0-9]+\\.[0-9]+\\.[0-9]+$"))
           | "\(.tag_name)\t\(.published_at | sub("\\.[0-9]+"; "") | fromdateiso8601)"' <<< "${body}" 2>/dev/null \
      || failed_lookup
    return
  fi
  body="$(fetch 'https://nodejs.org/dist/index.json')" || { failed_lookup; return 0; }
  jq -r --arg m "v${1%%.*}." '.[] | select(.lts) | select(.version | startswith($m))
         | "\(.version)\t\(.date + "T00:00:00Z" | fromdateiso8601)"' <<< "${body}" 2>/dev/null \
    || failed_lookup
}

# versions_python CURRENT: "<X.Y.Z>\t<epoch>" for the CPython builds setup-python
# publishes (actions/python-versions) in CURRENT's minor.
function versions_python() {
  local -r minor="$(cut -d. -f1-2 <<< "${1}")"
  local body
  body="$(github_releases actions/python-versions)" || { failed_lookup; return 0; }
  jq -r --arg m "${minor}." '.[] | select(.draft | not) | select(.prerelease | not)
         | (.tag_name | sub("-.*$"; "")) as $v | select($v | startswith($m)) | select($v | test("^[0-9]+\\.[0-9]+\\.[0-9]+$"))
         | "\($v)\t\(.published_at | sub("\\.[0-9]+"; "") | fromdateiso8601)"' <<< "${body}" 2>/dev/null \
    || failed_lookup
}

# versions_pypi PKG: "<version>\t<epoch>" for every final, unyanked release.
function versions_pypi() {
  local body
  if [ -z "${UPDATE_PINS_FIXTURES:-}" ]; then latest_first pypi "${1}" "${2:-}"; return; fi
  body="$(fetch "https://pypi.org/pypi/${1}/json")" || { failed_lookup; return 0; }
  jq -r '.releases | to_entries[] | select(.value | length > 0) | select(.key | test("^[0-9]+(\\.[0-9]+)*$"))
         | select(any(.value[]; .yanked) | not)
         | "\(.key)\t\(.value[0].upload_time_iso_8601 | sub("\\.[0-9]+"; "") | fromdateiso8601)"' <<< "${body}" 2>/dev/null \
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
      versions_npm "${source#npm:}" "${current}" | choose "${current}" "${COOLDOWN_DAYS}" ;;
    node)
      resolve_node "${current}" ;;
    python)
      resolve_python "${current}" ;;
    pypi)
      versions_pypi "${source#pypi:}" "${current}" | choose "${current}" "${COOLDOWN_DAYS}" ;;
    npmlock)
      versions_npm "${source#npm:}" "${current}" | choose "${current}" "${COOLDOWN_DAYS}" ;;
    runner)
      resolve_runner "${current}" ;;
    image)
      resolve_image "${name}" "${current}" ;;
    devimage)
      resolve_devimage "${name}" "${current}" "${source}" ;;
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

function resolve_python() {
  local -r current="${1}"
  if [[ "${current}" =~ ^[0-9]+\.[0-9]+$ ]]; then
    local latest
    latest="$(versions_python "${current}" | grep -v '^!fail' | sort -t$'\t' -k1,1V | tail -1 | cut -f1)"
    printf 'unpinned\t%s\t-\tminor only; pin the exact release\n' "${latest:--}"
    return
  fi
  versions_python "${current}" | choose "${current}" "${COOLDOWN_DAYS}"
}

# resolve_runner LABEL: a newer image of the same family and shape (the labels
# actions/runner-images lists) is report-only — an OS upgrade is never drop-in.
function resolve_runner() {
  local -r current="${1}"
  local body labels shape label best=''
  body="$(fetch 'https://raw.githubusercontent.com/actions/runner-images/main/README.md')" \
    || { printf 'unresolved\t-\t-\trunner-images lookup failed\n'; return; }
  labels="$(grep -oE '`[a-z]+-[0-9][A-Za-z0-9.-]*`' <<< "${body}" | tr -d '`' | sort -u)"
  if ! grep -qxF -- "${current}" <<< "${labels}"; then
    printf 'unresolved\t-\t-\tlabel not listed by actions/runner-images (retired?)\n'
    return
  fi
  shape="$(sed -E 's/-[0-9]+(\.[0-9]+)?/-N/' <<< "${current}")"
  while IFS= read -r label; do
    [ "$(sed -E 's/-[0-9]+(\.[0-9]+)?/-N/' <<< "${label}")" = "${shape}" ] || continue
    newer "$(runner_version "${label}")" "$(runner_version "${best:-${current}}")" && best="${label}"
  done <<< "${labels}"
  if [ -n "${best}" ]; then
    printf 'major\t%s\t-\tnewer image %s (report-only)\n' "${best}" "${best}"
  else
    printf 'current\t-\t-\t\n'
  fi
}

# runner_version LABEL: the OS version in a runner label (ubuntu-24.04 → 24.04).
function runner_version() {
  sed -E 's/^[a-z]+-([0-9]+(\.[0-9]+)?).*/\1/' <<< "${1}"
}

# resolve_devimage NAME PINNED TREE: current when the tree's build context is the pinned one; stale
# toward the tree's context once it is published (apply pins it by digest); until then a hold:
# run.sh builds that context locally.
function resolve_devimage() {
  local -r name="${1}" pinned="${2}" tree="${3}"
  if [ "${pinned}" = "${tree}" ]; then
    printf 'current\t-\t-\t\n'
  elif image_digest "$(devimage_repository "${name}"):${tree}" > /dev/null; then
    printf 'stale\t%s\t-\tpublished; apply pins it by digest\n' "${tree}"
  else
    printf 'hold\t%s\t-\tnot published yet; run.sh builds it locally\n' "${tree}"
  fi
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

# canonical_owner LOCS: in a consumer, a pin located in a canonical file (byte-identical to its
# .standards copy) is .standards' to refresh: its release carries it, and the consumer never
# gates on or applies it. Prints the hold reason when any of LOCS (file:line,...) is canonical.
function canonical_owner() {
  [ -d .standards ] || return 1
  local loc file
  local -a all=()
  IFS=',' read -r -a all <<< "${1}"
  for loc in "${all[@]}"; do
    file="${loc%%:*}"
    if [ -f ".standards/${file}" ] && cmp -s "${file}" ".standards/${file}"; then
      printf 'held by .standards (canonical %s)' "${file}"
      return 0
    fi
  done
  return 1
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
# resolve_all PINS: every pin resolved, UPDATE_PINS_RESOLVE_JOBS (default 16) at a time — each lookup
# is network-bound — in the order the pins came in.
function resolve_all() {
  local -r jobs="${UPDATE_PINS_RESOLVE_JOBS:-16}"
  local tmp row i=0
  tmp="$(mktemp -d)"
  while IFS= read -r row; do
    [ -n "${row}" ] || continue
    i=$(( i + 1 ))
    while [ "$(jobs -rp | wc -l)" -ge "${jobs}" ]; do sleep 0.2; done
    resolve_row "${row}" > "${tmp}/$(printf '%05d' "${i}")" &
  done <<< "${1}"
  wait
  cat "${tmp}"/* 2> /dev/null || true
  rm -rf "${tmp}"
}

# resolve_row ROW: one discovered pin with its status, candidate, age and note.
function resolve_row() {
  local kind name loc current source locs status cand age note reason
  IFS=$'\t' read -r kind name loc current source locs <<< "${1}"
  if reason="$(held "${name}")" || reason="$(canonical_owner "${locs:-${loc}}")"; then
    status='hold'
    cand='-'
    age='-'
    note="${reason}"
  else
    IFS=$'\t' read -r status cand age note < <(resolve "${kind}" "${name}" "${current}" "${source}")
  fi
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "${kind}" "${name}" "${loc}" "${current}" "${source}" \
    "${status}" "${cand}" "${age}" "${note:--}" "${locs}"
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
# write_pins_md RESOLVED: every pin and floating input as a Markdown table (docs/current_pins.md).
function write_pins_md() {
  printf '# Current pins\n\n'
  printf 'Every pinned external input of this repository, the newest eligible candidate, and what still floats.\n'
  printf 'Generated by `make pins-doc` (`scripts/release/update-pins.sh`); regenerate it, never edit it.\n\n'
  printf '| Kind | Pin | Pinned | Status | Candidate | Pinned in |\n| --- | --- | --- | --- | --- | --- |\n'
  local kind name loc current source status cand rest
  while IFS=$'\t' read -r kind name loc current source status cand rest; do
    [ -n "${kind}" ] || continue
    printf '| %s | `%s` | `%s` | %s | %s | `%s` |\n' "${kind}" "${name}" "${current}" "${status}" \
      "$( [ "${cand}" = '-' ] && printf '-' || printf '`%s`' "${cand}")" "${loc}"
  done < <(sort -t$'\t' -k1,1 -k2,2 <<< "${1}")
}

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
  # Each pin verifies alone in its own worktree, UPDATE_PINS_JOBS (default 4) at a time.
  local -r jobs="${UPDATE_PINS_JOBS:-4}" resdir="${WT}.results"
  mkdir -p "${resdir}"
  local results='' kind name loc current source status cand age note locs origin logf i=0
  local -a queued=()
  while IFS=$'\t' read -r -u 3 kind name loc current source status cand age note locs origin; do
    i=$(( i + 1 ))
    logf="${LOGDIR}/$(printf '%02d' "${i}")-${kind}-$(printf '%s' "${name}" | tr -c 'A-Za-z0-9._-' '_').log"
    log "🔍 ${kind} ${name} ${current} → ${cand}"
    while [ "$(jobs -rp | wc -l)" -ge "${jobs}" ]; do sleep 1; done
    queued+=("$(printf '%s\t%s\t%s\t%s\t%s\t%s\t%s' "${kind}" "${name}" "${current}" "${cand}" "${source}" "${locs}" "${logf}")")
    verify_in_own_worktree "${resdir}/$(printf '%03d' "${i}")" "${kind}" "${name}" "${current}" "${cand}" \
      "${source}" "${locs}" "${logf}" &
  done 3<<< "${stale}"
  wait
  # A failed pin goes to the back of the queue once: a transient failure (network, image
  # builder) gets a second run after every other pin has had its first.
  local n=0 row
  for row in "${queued[@]}"; do
    n=$(( n + 1 ))
    awk -F'\t' '$7 == "fail" { f = 1 } END { exit !f }' "${resdir}/$(printf '%03d' "${n}")" 2>/dev/null || continue
    IFS=$'\t' read -r kind name current cand source locs logf <<< "${row}"
    log "🔁 retrying ${kind} ${name} ${current} → ${cand}"
    printf '\n--- retry ---\n' >> "${logf}"
    while [ "$(jobs -rp | wc -l)" -ge "${jobs}" ]; do sleep 1; done
    verify_in_own_worktree "${resdir}/$(printf '%03d' "${n}")" "${kind}" "${name}" "${current}" "${cand}" \
      "${source}" "${locs}" "${logf}" &
  done
  wait
  results="$(cat "${resdir}"/* 2>/dev/null)"$'\n'
  rm -rf "${resdir}"
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

# setup_worktree: a detached worktree of the staged tree with its submodules checked out
# from the local clones (no network), plus a private tool bin and npm prefix so
# verification never replaces a tool the developer has installed.
function setup_worktree() {
  add_worktree
  LOGDIR="${UPDATE_PINS_LOGDIR:-${WT}.logs}"
  mkdir -p "${LOGDIR}"
  LOGDIR="$(cd "${LOGDIR}" && pwd)"
  trap cleanup_worktree EXIT
  trap interrupted INT TERM HUP
  log "🧪 verifying in ${WT} (logs: ${LOGDIR})"
}

# interrupted: stop everything this run started — every verification process below this one
# (make, run.sh, the tool containers' clients) — then remove every worktree it created, so an
# interrupted run leaves nothing running and nothing checked out.
function interrupted() {
  trap - INT TERM HUP
  log '⛔ interrupted: stopping verifications and removing their worktrees'
  kill_tree "$$"
  local wt
  while IFS= read -r wt; do
    git -C "${ROOT}" worktree remove --force "${wt}" > /dev/null 2>&1 || true
    rm -rf "${wt}.tools"
  done < <(git -C "${ROOT}" worktree list --porcelain \
    | awk '$1 == "worktree" && $2 ~ /\/update-pins\.[A-Za-z0-9]+$/ { print $2 }')
  git -C "${ROOT}" worktree prune
  exit 130
}

# kill_tree PID: TERM every descendant of PID, deepest first (PID itself is left running).
function kill_tree() {
  local child
  for child in $(pgrep -P "${1}" 2> /dev/null || true); do
    kill_tree "${child}"
    kill -TERM "${child}" 2> /dev/null || true
  done
}

# add_worktree: WT, a detached worktree of the staged tree — HEAD plus the index, so
# verification sees what the next commit holds — with its submodules at their staged commits
# plus their uncommitted changes to tracked files, and TOOLS beside it.
function add_worktree() {
  WT="$(mktemp -d "${TMPDIR:-/tmp}/update-pins.XXXXXX")"
  TOOLS="${WT}.tools"
  mkdir -p "${TOOLS}/bin" "${TOOLS}/npm"
  rmdir "${WT}"
  git worktree add --quiet --detach "${WT}" HEAD
  git -C "${ROOT}" diff --cached --binary --no-color | git -C "${WT}" apply --index --allow-empty --whitespace=nowarn
  local key path name
  while read -r key path; do
    [ -n "${key}" ] || continue
    name="${key#submodule.}"
    name="${name%.path}"
    git -C "${WT}" -c protocol.file.allow=always -c "submodule.${name}.url=${ROOT}/${path}" \
      submodule update --init --quiet -- "${path}"
    git -C "${ROOT}/${path}" diff HEAD --binary --no-color | git -C "${WT}/${path}" apply --allow-empty --whitespace=nowarn
  done < <(git config -f .gitmodules --get-regexp '^submodule\..*\.path$' 2>/dev/null || true)
}

# verify_in_own_worktree RESULT_FILE KIND NAME CURRENT CAND SOURCE LOCS LOG: verify_pin in a
# worktree of its own (removed afterwards); writes the result row to RESULT_FILE.
function verify_in_own_worktree() {
  local -r resfile="${1}"
  shift
  (
    trap 'git -C "${ROOT}" worktree remove --force "${WT}" > /dev/null 2>&1; rm -rf "${TOOLS}"' EXIT
    add_worktree
    local result
    result="$(verify_pin "$@")" || result="$(printf 'fail\tverification error')"
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "${1}" "${2}" "${3}" "${4}" "${5}" "${6}" "${result}" "${7}" \
      > "${resfile}"
  )
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
    devimage)
      printf 'audit' ;;
    # Tool installs run in the tool image (Linux) on every host.
    gotool | npm | tool)
      if [ -n "${setup}" ]; then printf '%s' "${setup}"; else combined_targets; fi ;;
    # The tool image is Linux on every host, so the toolchain is tested locally everywhere.
    toolchain)
      printf 'test' ;;
    node | npmlock)
      printf 'lint' ;;
    python | pypi)
      if has_target release-pypi; then printf 'release-pypi'; else combined_targets; fi ;;
    # Runner images exist only on GitHub-hosted runners: the one kind left to CI.
    runner)
      printf 'verify-on-ci' ;;
    # An image pin, and any kind without a specific target: the project's combined run builds
    # and runs what uses it.
    *)
      combined_targets ;;
  esac
}

function verify_on_ci_reason() {
  case "${1}" in
    tool) printf 'no setup target installs this tool' ;;
    toolchain) printf 'the toolchain switch is exercised on the CI runner' ;;
    image) printf 'image builds run in the CI stacks' ;;
    python | pypi) printf 'no release-pypi target builds the package' ;;
    runner) printf 'runner images exist only on GitHub-hosted runners' ;;
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
    devimage) (cd "${root}" && bash scripts/dev/publish.sh ref) ;;
    tool) replace_at "${root}" "${locs}" "${current}" "${new}" && refresh_checksums "${root}" "${locs}" "${source#github:}" "${current}" "${cand}" \
      && refresh_commits "${root}" "${locs}" "${source#github:}" "${cand}" ;;
    gotool | toolchain | npm | python | runner) replace_at "${root}" "${locs}" "${current}" "${new}" ;;
    node) replace_at "${root}" "${locs}" "${current}" "${new}" && follow_node_images "${root}" "${current}" "${new}" ;;
    gomod) apply_gomod "${root}" "${name}" "${cand}" "${locs}" ;;
    pypi) replace_at "${root}" "${locs}" "${current}" "${new}" && refresh_pypi_hashes "${root}" "${locs}" "${name}" "${new}" ;;
    npmlock) replace_at "${root}" "${locs}" "${current}" "${new}" && relock_npm "${root}" "${locs}" ;;
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

# follow_node_images ROOT CURRENT NEW: every `node:CURRENT-<variant>@sha256:<digest>` image
# moves to NEW with that tag's current digest, so the image that supplies Node stays in step
# with .nvmrc.
function follow_node_images() {
  local -r root="${1}" cur="${2#v}" new="${3#v}"
  local ref variant digest
  while IFS= read -r ref; do
    [ -n "${ref}" ] || continue
    variant="${ref#"node:${cur}"}"
    variant="${variant%%@*}"
    digest="$(image_digest "node:${new}${variant}")"
    [[ "${digest}" =~ ^sha256:[0-9a-f]{64}$ ]] || { printf 'no digest for node:%s%s\n' "${new}" "${variant}"; return 1; }
    replace_all "${root}" "${ref}" "node:${new}${variant}@${digest}"
    printf 'node:%s%s → %s\n' "${new}" "${variant}" "${digest}"
  done < <(cd "${root}" && source_files | xargs grep -ohE "node:${cur//./\\.}(-[a-z0-9.-]+)?@sha256:[0-9a-f]{64}" 2>/dev/null | sort -u)
}

# refresh_commits ROOT LOCS REPO CAND: every committed `*_COMMIT=<sha>` in the
# pin's files becomes the commit tag CAND points at (a tool cloned at its
# release tag and verified against that commit).
function refresh_commits() {
  local -r root="${1}" locs="${2}" repo="${3}" cand="${4}"
  local loc file sha all=()
  IFS=',' read -r -a all <<< "${locs}"
  for loc in "${all[@]}"; do
    file="${loc%%:*}"
    grep -qE '[A-Z0-9_]+_COMMIT=["'"'"']?[0-9a-f]{40}' "${root}/${file}" || continue
    sha="$(fetch "https://api.github.com/repos/${repo}/commits/${cand}" | jq -r '.sha // empty')"
    [[ "${sha}" =~ ^[0-9a-f]{40}$ ]] || { printf 'no commit for %s@%s\n' "${repo}" "${cand}"; return 1; }
    SHA="${sha}" perl -pi -e 's/([A-Z0-9_]+_COMMIT=["\x27]?)[0-9a-f]{40}/$1$ENV{SHA}/' "${root}/${file}"
    printf '%s: commit %s (%s@%s)\n' "${file}" "${sha}" "${repo}" "${cand}"
  done
}

# refresh_pypi_hashes ROOT LOCS PKG VERSION: in each requirements file among
# LOCS, the --hash lines under PKG==VERSION become the sha256 of every file PyPI
# publishes for that release.
function refresh_pypi_hashes() {
  local -r root="${1}" locs="${2}" pkg="${3}" version="${4}"
  local loc file hashes all=()
  hashes="$(fetch "https://pypi.org/pypi/${pkg}/${version}/json" | jq -r '.urls[].digests.sha256' | sort -u)"
  [ -n "${hashes}" ] || { printf 'no files published for %s %s\n' "${pkg}" "${version}"; return 1; }
  IFS=',' read -r -a all <<< "${locs}"
  for loc in "${all[@]}"; do
    file="${loc%%:*}"
    [[ "${file}" == *requirements*.txt ]] || continue
    PKG="${pkg}" VER="${version}" HASHES="${hashes}" perl -0pi -e '
      my $h = join(" \\\n", map { "    --hash=sha256:$_" } split /\n/, $ENV{HASHES});
      s/^(\Q$ENV{PKG}\E==\Q$ENV{VER}\E)[ \t]*\\\n(?:[ \t]+--hash=sha256:[0-9a-f]{64}[ \t]*\\?\n)+/$1 \\\n$h\n/m' "${root}/${file}"
    grep -qF -- "${pkg}==${version} \\" "${root}/${file}" \
      || { printf '%s: %s==%s hash block not rewritten\n' "${file}" "${pkg}" "${version}"; return 1; }
  done
}

# relock_npm ROOT LOCS: regenerate the package-lock.json next to each
# package.json among LOCS, resolving only releases outside the cooldown.
function relock_npm() {
  local -r root="${1}" locs="${2}"
  local loc dir before all=()
  before="$(jq -rn --argjson t "$(( NOW - COOLDOWN_DAYS * DAY ))" '$t | todate')"
  IFS=',' read -r -a all <<< "${locs}"
  for loc in "${all[@]}"; do
    dir="$(dirname "${root}/${loc%%:*}")"
    (cd "${dir}" && npm install --package-lock-only --ignore-scripts --no-audit --no-fund --before="${before}") \
      || { printf 'npm lock regeneration failed in %s\n' "${dir}"; return 1; }
  done
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
    python) printf 'unpatched CPython security fixes in the publish job' ;;
    pypi) printf 'build-backend fixes and metadata-format support (an index rejects metadata a stale backend writes)' ;;
    npmlock) printf 'linter rule fixes; vulnerable packages kept in the locked tree' ;;
    runner) printf 'a retired image stops scheduling jobs; its preinstalled toolchains age' ;;
    *) printf 'unknown' ;;
  esac
}

main "${@:-}"
