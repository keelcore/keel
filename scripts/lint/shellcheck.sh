#!/usr/bin/env bash
# scripts/lint/shellcheck.sh
# Runs shellcheck against all bash scripts in the repository.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# shellcheck source=../lib/paths-ensure.sh
. "$(dirname "${BASH_SOURCE[0]}")/../lib/paths-ensure.sh"
# shellcheck source=../lib/paths.sh
. "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"

function main() {
  exec 5>&1
  validate_env
  lint
}

function validate_env() {
  # `bash scripts/X.sh` does not source the interactive shell's init files;
  # restore Homebrew / system-wide PATH additions so brew-installed tools
  # are findable when run from the bash subshell.
  paths_ensure_standard

  log 'Checking for shellcheck...'
  if ! command -v shellcheck > /dev/null 2>&1; then
    log '❌ shellcheck not found on PATH and not present at standard install locations.'
    log '   Install via: brew install shellcheck (macOS) or apt-get install shellcheck (Debian/Ubuntu).'
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
  # git ls-files honors .gitignore (so .scratch/ is naturally excluded) and
  # only lists tracked files (no .standards/ submodule duplication).
  git ls-files -z '*.sh' \
    | nolint_filter0 \
    | xargs -0 -n 1 realpath \
    | sort -u \
    | xargs shellcheck --shell=bash --severity=warning \
    || rc="${?}"
  if [ "${rc}" -ne 0 ]; then
    log "❌ shellcheck failed (exit ${rc})"
    exit "${rc}"
  fi
  log '✅ All scripts passed shellcheck'
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/shellcheck.log' >&5
}

main "${@:-}"
