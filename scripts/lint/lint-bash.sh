#!/usr/bin/env bash
# scripts/lint/lint-bash.sh
# Runs shellcheck against all bash scripts in the repository, and checks BATS files for a [[ … ]] that is a
# test's own statement but not its last, without `|| false`: bash does not always fail a test on it. Every
# repo-owned *.sh and *.bash file imports the environment variables it reads per governance/bash.md R37, and
# sources files with `source`, not `.` (check_env_imports, scripts/lib/env-imports.sh).
# Project hook: lint-bash.local.sh runs first, when present, with this script's arguments
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

# shellcheck source=../lib/paths-ensure.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/paths-ensure.sh"
# shellcheck source=../lib/paths.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"
# shellcheck source=../lib/env-imports.sh
source "$(dirname "${BASH_SOURCE[0]}")/../lib/env-imports.sh"

function main() {
  exec 5>&1
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  validate_env
  lint
  check_bats_conditionals
  check_env
}

function validate_env() {
  # `bash scripts/X.sh` does not source the interactive shell's init files;
  # restore Homebrew / system-wide PATH additions so brew-installed tools
  # are findable when run from the bash subshell.
  paths_ensure_standard

  log 'Checking for shellcheck...'
  if ! command -v shellcheck > /dev/null 2>&1; then
    log '❌ shellcheck not found on PATH and not present at standard install locations.'
    log '   shellcheck comes with the dev image; run: make lint-bash'
    exit 1
  fi
  log "✅ shellcheck available ($(command -v shellcheck))"
}

function lint() {
  log 'Running shellcheck on all scripts...'
  local rc
  rc=0
  # Resolve symlinks so shellcheck reads each script via its real path. This
  # matters for `# shellcheck source=...` directives, which resolve relative
  # to the script file's location. A symlinked entry would otherwise pin
  # SCRIPTDIR to the wrong tree.
  # Cover ALL tracked *.sh in the repo so consumer feature scripts (e.g.,
  # macos/features/, raspberry_pi/scripts/) get the same lint as helpers.
  # source_files lists repo-owned tracked *.sh with vendored trees and
  # submodules excluded (never a raw git ls-files, which includes vendor/).
  # Paths travel one per line and NUL-delimited into shellcheck, so names
  # containing spaces stay whole.
  source_files '*.sh' \
    | while IFS= read -r f; do realpath "${f}"; done \
    | sort -u \
    | tr '\n' '\0' \
    | xargs -0 shellcheck --shell=bash --severity=warning \
    || rc="${?}"
  if [ "${rc}" -ne 0 ]; then
    log "❌ shellcheck failed (exit ${rc})"
    exit "${rc}"
  fi
  log '✅ All scripts passed shellcheck'
}

# check_bats_conditionals: every tracked *.bats file's tests; a top-level [[ … ]] statement (two-space
# indent) that is not the test's last statement must end with `|| false`. Heredoc text is not code.
function check_bats_conditionals() {
  local -a files=()
  local f
  while IFS= read -r f; do files+=("${f}"); done < <(source_files '*.bats')
  [ "${#files[@]}" -gt 0 ] || return 0
  local found
  found="$(awk '
    FNR == 1 { inside = 0; doc = "" }
    doc != "" { if ($0 == doc) doc = ""; next }
    /^@test .*\{[[:space:]]*$/ { inside = 1; n = 0; next }
    inside && /^\}[[:space:]]*$/ {
      for (i = 1; i < n; i++) if (cond[i]) printf "%s:%d: [[ … ]] is not the last statement; end it with `|| false`\n", FILENAME, line[i]
      inside = 0; next
    }
    inside {
      if (match($0, /<<-?[[:space:]]*["\047]?[A-Za-z_]+["\047]?/)) { doc = substr($0, RSTART, RLENGTH); sub(/^<<-?[[:space:]]*["\047]?/, "", doc); sub(/["\047]$/, "", doc) }
      if ($0 ~ /^[[:space:]]*$/ || $0 ~ /^[[:space:]]*#/) next
      if ($0 !~ /^  [^ ]/) next
      n++; line[n] = FNR; cond[n] = ($0 ~ /^  \[\[ .* \]\][[:space:]]*$/)
    }
  ' "${files[@]}")"
  if [ -n "${found}" ]; then
    printf '%s\n' "${found}" | while IFS= read -r f; do log "❌ ${f}"; done
    exit 1
  fi
  log "✅ BATS conditionals OK (${#files[@]} files)"
}

# check_env: every repo-owned *.sh and *.bash file (not *.bats: a test sets the environment for what it runs)
# conforms to R37 and sources with `source`.
function check_env() {
  local -a files=()
  local f found
  while IFS= read -r f; do files+=("${f}"); done < <(source_files '*.sh' '*.bash')
  found="$(check_env_imports "${files[@]}")"
  if [ -n "${found}" ]; then
    printf '%s\n' "${found}" | while IFS= read -r f; do log "❌ ${f}"; done
    log "❌ $(printf '%s\n' "${found}" | wc -l | tr -d ' ') environment-import findings (governance/bash.md R37)"
    exit 1
  fi
  log "✅ Environment imports OK (${#files[@]} files, governance/bash.md R37)"
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/shellcheck.log' >&5
}

main "${@:-}"
