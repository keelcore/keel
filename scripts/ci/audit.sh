#!/usr/bin/env bash
# audit.sh
# CI/standards compliance auditor — a GATE. Enforces six invariants:
#   1. Every workflow run: step is `make <target>` — no direct tool calls.
#   2. Every scripts/**/*.sh (except scripts/lib/) has a Makefile target.
#   3. Universal canonical targets (build, lint, test, test-unit,
#      test-integration, clean, audit) are defined in the Makefile.
#   4. Every Makefile target has at most one active recipe line, and that
#      line invokes a single scripts/**/*.sh script. @echo / blank / # are
#      ignored; multi-step orchestration belongs in scripts/, not Makefile.
#   5. Every Makefile target's recipe-invoked scripts/**/*.sh file exists
#      on disk (no broken references; catches templates copied with stale
#      script paths).
#   6. No runtime tool installs in workflows (`actions/setup-*`, `make setup-*`):
#      tools come from the tool image; host jobs are listed in .runtime-install-exceptions.
#   7. SECURITY.md, CODEOWNERS and dependabot.yml exist.
#   8. Targets are verb-first (<verb>-<object>[-<qualifier>]).
#   9. Every script is named verb-first, as its target is (governance/ci.md, Make Targets).
#  10. Every canonical script runs its project hook (scripts/lib/local-hook.sh), or its own <script>.local.sh.
#
# Exit semantics:
#   - Exit 0 only when every invariant holds; exit 1 when any finding is
#     printed. `make audit` is a required CI step (governance/ci.md), so a
#     finding fails the build.
#   - Hard errors (malformed args, unreadable Makefile) also exit non-zero.
#   - A missing .github/workflows/ directory is NOT a finding: consumers may
#     use a different CI provider or not yet have CI wired up; Rule 1 skips.
# Safe to run locally: make audit
# Project hook: audit.local.sh runs first, when present, with this script's arguments
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

REPO_ROOT="$(git rev-parse --show-toplevel)"
readonly REPO_ROOT
readonly MAKEFILE="${REPO_ROOT}/Makefile"
readonly WORKFLOWS_DIR="${REPO_ROOT}/.github/workflows"
readonly SCRIPTS_DIR="${REPO_ROOT}/scripts"

# Populated by build_expanded_makefile: a temp file containing the consumer
# Makefile with all `include` / `-include` / `sinclude` directives inlined
# recursively. Rules 2–5 read this expanded view so they see canonical
# targets inherited via include.
EXPANDED_MAKEFILE=''

# excluded_scripts prints the canonical scripts a consumer opts out of, read from
# <root>/.canonical-exclude: one repo-relative path per line; blank lines and
# # comments are ignored. Excluded scripts are neither synced nor verified, and
# audit Rule 5 accepts their canonical targets pointing at a missing file.
function excluded_scripts() {
  local -r file="${1}/.canonical-exclude"
  [ -f "${file}" ] || return 0
  sed -e 's/#.*//' -e 's/[[:space:]]*$//' "${file}" | grep -v '^$' || true
}

function is_excluded() {
  excluded_scripts "${REPO_ROOT}" | grep -qxF "${1}"
}

function log() {
  printf '%s\n' "${1:-}"
}

function validate_args() {
  if [ "${#}" -gt 0 ] && [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected arg'
    exit 1
  fi
}

# Inline include directives recursively. Paths are resolved relative to
# REPO_ROOT (Make's CWD). Missing files and Make-variable-bearing paths
# are silently skipped. Cycles broken by a colon-delimited visited set.
function _inline_includes() {
  local -r src="${1}"
  local -r visited="${2}"
  local line
  while IFS= read -r line || [ -n "${line}" ]; do
    if [[ "${line}" =~ ^[[:space:]]*(-?include|sinclude)[[:space:]]+(.+)$ ]]; then
      _inline_include_paths "${BASH_REMATCH[2]}" "${visited}"
      continue
    fi
    printf '%s\n' "${line}"
  done < "${src}"
}

function _inline_include_paths() {
  local -r paths="${1}"
  local -r visited="${2}"
  local p
  for p in ${paths}; do
    [[ "${p}" == *'$'* ]] && continue
    local full="${REPO_ROOT}/${p}"
    [ -f "${full}" ] || continue
    [[ "${visited}" == *":${full}:"* ]] && continue
    _inline_includes "${full}" "${visited}:${full}:"
  done
}

function build_expanded_makefile() {
  EXPANDED_MAKEFILE="$(mktemp -t audit-make-expanded.XXXXXX)"
  _inline_includes "${MAKEFILE}" ":${MAKEFILE}:" > "${EXPANDED_MAKEFILE}"
}

function cleanup_expanded_makefile() {
  if [ -n "${EXPANDED_MAKEFILE}" ] && [ -f "${EXPANDED_MAKEFILE}" ]; then
    rm -f "${EXPANDED_MAKEFILE}"
  fi
}

# ---------------------------------------------------------------------------
# Rule 1: All workflow run: steps must be `make <target>`.
#
# Consumers may legitimately have no GitHub Actions workflows (different CI
# provider, or CI not yet wired up). Treat a missing workflows directory as
# "nothing to audit" rather than a hard error — a hard error here used to
# bury Rules 2–5 under a stray `grep: No such file or directory` message.
# ---------------------------------------------------------------------------
function check_workflow_run_steps() {
  if [ ! -d "${WORKFLOWS_DIR}" ]; then
    return 0
  fi

  local failed=0
  local violations

  # Match lines of the form "        run: <something>" where <something> is not "make".
  # Output from grep -rn includes "file:linenum:content" — the second grep matches
  # against the full line so we omit the ^ anchor from the exclusion pattern.
  violations="$(
    grep -rn '^\s*run:' "${WORKFLOWS_DIR}/" \
      | grep -v 'run:\s*make\s' \
      || true
  )"

  if [ -n "${violations}" ]; then
    log '❌ Rule 1: workflow run: steps must be `make <target>` — violations:'
    while IFS= read -r line; do
      log "  ${line}"
    done <<< "${violations}"
    failed=1
  fi

  return "${failed}"
}

# ---------------------------------------------------------------------------
# Rule 2: Every scripts/**/*.sh (except scripts/lib/) must appear in Makefile.
# ---------------------------------------------------------------------------
function check_script_targets() {
  local failed=0

  while IFS= read -r script_abs; do
    # Compute path relative to REPO_ROOT (strip leading path + /)
    local script_rel
    script_rel="${script_abs#"${REPO_ROOT}/"}"

    # Skip sourced library files — they are not standalone executables.
    if [[ "${script_rel}" == scripts/lib/* ]]; then
      continue
    fi

    # Check if the script path appears anywhere in the (expanded) Makefile.
    if ! grep -qF "${script_rel}" "${EXPANDED_MAKEFILE}"; then
      log "❌ Rule 2: no Makefile target invokes: ${script_rel}"
      failed=1
    fi
  done < <(find "${SCRIPTS_DIR}" -name '*.sh' | sort)

  return "${failed}"
}

# ---------------------------------------------------------------------------
# Rule 3: Universal canonical targets must exist.
# ---------------------------------------------------------------------------
readonly -a UNIVERSAL_TARGETS=(
  audit
  build
  clean
  lint
  test
  test-integration
  test-unit
)

function check_universal_targets() {
  local failed=0

  for target in "${UNIVERSAL_TARGETS[@]}"; do
    # A target is defined if the (expanded) Makefile contains a line
    # starting with `<target>:`.
    if ! grep -qE "^${target}:" "${EXPANDED_MAKEFILE}"; then
      log "❌ Rule 3: universal Makefile target missing: ${target}"
      failed=1
    fi
  done

  return "${failed}"
}

# ---------------------------------------------------------------------------
# Rule 4: Each Makefile target has at most one active recipe line, and that
# line invokes a single scripts/**/*.sh script.
# ---------------------------------------------------------------------------
function check_recipe_shape() {
  local failed=0
  local current_target=''
  local active_count=0
  local active_line=''
  local buf=''
  local raw_line line body

  while IFS= read -r raw_line || [ -n "${raw_line}" ]; do
    # Accumulate line continuations: a trailing backslash joins with the next line.
    if [[ "${raw_line}" == *\\ ]]; then
      buf="${buf}${raw_line%\\} "
      continue
    fi
    line="${buf}${raw_line}"
    buf=''

    # Target header: identifier at column 0 followed by ":" and NOT a variable assignment.
    if [[ "${line}" =~ ^[a-zA-Z_][a-zA-Z0-9_.-]*[[:space:]]*: ]] \
       && [[ ! "${line}" =~ ^[a-zA-Z_][a-zA-Z0-9_.-]*[[:space:]]*[?:+]?= ]]; then
      # Evaluate previous target before starting a new one.
      if [ -n "${current_target}" ]; then
        _eval_recipe_shape "${current_target}" "${active_count}" "${active_line}" \
          || failed=1
      fi
      current_target="${line%%:*}"
      # Trim trailing whitespace from target name.
      current_target="${current_target%"${current_target##*[![:space:]]}"}"
      active_count=0
      active_line=''
      continue
    fi

    # Recipe body line: starts with TAB.
    if [[ "${line}" == $'\t'* ]]; then
      [ -z "${current_target}" ] && continue
      body="${line#$'\t'}"
      while [[ "${body}" == $'\t'* ]]; do body="${body#$'\t'}"; done
      # Skip blank, comment, @echo-only lines.
      [ -z "${body}" ] && continue
      [[ "${body}" == \#* ]] && continue
      [[ "${body}" =~ ^@?-?echo([[:space:]]|$) ]] && continue
      # Strip leading @ and - modifiers (echo-suppression / error-ignore).
      while [[ "${body}" =~ ^[@-] ]]; do body="${body:1}"; done
      active_count=$((active_count + 1))
      active_line="${body}"
      continue
    fi

    # Blank or other unindented line: target body terminator.
    if [ -z "${line}" ] && [ -n "${current_target}" ]; then
      _eval_recipe_shape "${current_target}" "${active_count}" "${active_line}" \
        || failed=1
      current_target=''
      active_count=0
      active_line=''
    fi
  done < "${EXPANDED_MAKEFILE}"

  # Evaluate the last target if the file did not end with a blank line.
  if [ -n "${current_target}" ]; then
    _eval_recipe_shape "${current_target}" "${active_count}" "${active_line}" \
      || failed=1
  fi

  return "${failed}"
}

function _eval_recipe_shape() {
  local -r tgt="${1}"
  local -r count="${2}"
  local -r cmd="${3}"
  local stripped

  if [ "${count}" -eq 0 ]; then
    return 0
  fi

  if [ "${count}" -gt 1 ]; then
    log "❌ Rule 4: target '${tgt}' has ${count} active recipe lines; max is 1."
    log '         Aggregate the steps into a single scripts/**/*.sh and call it.'
    return 1
  fi

  # Exactly 1 active line. Strip leading "VAR=value " env-var prefixes, then
  # require the call to invoke a scripts/**/*.sh script. Accept all standard
  # script invocation forms:
  #   - `bash scripts/X/Y.sh ...`              (explicit interpreter)
  #   - `sh scripts/X/Y.sh ...`                (POSIX interpreter)
  #   - `./scripts/X/Y.sh ...`                 (executable; relies on shebang)
  #   - `scripts/X/Y.sh ...`                   (executable via PATH/cwd resolution)
  # An optional `.standards/` prefix is accepted because standards-only
  # scripts (not shipped to consumers — e.g. refresh-governance.sh) are
  # invoked from the consumer Makefile via the submodule path.
  # Env-var prefix forms accepted (each may repeat):
  #   - VAR=unquoted_value
  #   - VAR='single quoted value with spaces'
  #   - VAR="double quoted value with spaces"
  stripped="${cmd}"
  while [[ "${stripped}" =~ ^[A-Z_][A-Z0-9_]*=(\'[^\']*\'|\"[^\"]*\"|[^[:space:]\'\"]+)[[:space:]]+ ]]; do
    stripped="${stripped#"${BASH_REMATCH[0]}"}"
  done

  # A recipe that runs its tool in a tool image (`bash scripts/dev/run.sh [--writable]
  # [--image NAME] bash scripts/X.sh ...`) is checked for the runner, then for the script
  # it runs.
  local -r runner_re='^bash[[:space:]]+scripts/dev/run\.sh([[:space:]]+--writable|[[:space:]]+--image[[:space:]]+[^[:space:]]+)*[[:space:]]+'
  if [[ "${stripped}" =~ ${runner_re} ]]; then
    if [ ! -f "${REPO_ROOT}/scripts/dev/run.sh" ] && ! is_excluded 'scripts/dev/run.sh'; then
      log "❌ Rule 5: target '${tgt}' invokes scripts/dev/run.sh which does not exist on disk."
      return 1
    fi
    stripped="${stripped#"${BASH_REMATCH[0]}"}"
    # A shell in the image (run-dev-shell) runs no repository script.
    [ "${stripped}" = 'bash' ] && return 0
  fi

  if [[ "${stripped}" =~ ^(bash[[:space:]]+|sh[[:space:]]+|\.?/?)((\.standards/)?scripts/[^[:space:]]+\.sh) ]]; then
    # Rule 4 satisfied. Now Rule 5: does the referenced script exist on disk?
    # The canonical Makefile (templates/Makefile.canonical) only references
    # scripts that ARE shipped to consumers by refresh-governance, so a
    # broken Rule 5 finding always points to a real bug — either consumer
    # drift or a missing canonical script — never to a known-standards-only
    # script masquerading as consumer-facing.
    # Exception: a script the consumer lists in .canonical-exclude is absent on
    # purpose, so its canonical target is expected to point at nothing.
    local script_path="${BASH_REMATCH[2]}"
    if [ ! -f "${REPO_ROOT}/${script_path}" ] && ! is_excluded "${script_path}"; then
      log "❌ Rule 5: target '${tgt}' invokes ${script_path} which does not exist on disk."
      return 1
    fi
    return 0
  fi

  log "❌ Rule 4: target '${tgt}' active line must invoke a scripts/**/*.sh script, got:"
  log "         ${cmd}"
  log "         Accepted forms: 'bash scripts/X.sh', 'sh scripts/X.sh', './scripts/X.sh', 'scripts/X.sh' (optionally prefixed with '.standards/')"
  return 1
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
# Rule 6: No runtime installs in workflows. Tools come from the tool image, built
# once and pinned by digest: a job neither runs `actions/setup-<tool>` nor a
# `make setup-<tool>` step. A job that genuinely runs on the host (a native
# macOS/Windows runner, a step that drives docker or a cluster on the runner) is
# listed in <root>/.runtime-install-exceptions as `<workflow file>:<job id>`,
# followed by its reason (blank lines and # comments ignored). The canonical CodeQL job compiles
# with Go on the runner in every repository, so it is listed here.
# ---------------------------------------------------------------------------
function check_runtime_installs() {
  [ -d "${WORKFLOWS_DIR}" ] || return 0
  local -r exceptions="${REPO_ROOT}/.runtime-install-exceptions"
  local failed=0 wf job line
  while IFS=$'\t' read -r wf job line; do
    [ -n "${wf}" ] || continue
    [ "${wf}:${job}" != '.github/workflows/codeql.yml:analyze' ] || continue
    if [ -f "${exceptions}" ] && sed -e 's/#.*//' "${exceptions}" | awk '{ print $1 }' | grep -qxF "${wf}:${job}"; then
      continue
    fi
    [ "${failed}" -eq 1 ] || log '❌ Rule 6: runtime tool installs in workflows (tools come from the tool image; list a host job in .runtime-install-exceptions):'
    log "  ${wf}:${job}: ${line}"
    failed=1
  done < <(runtime_install_steps)
  return "${failed}"
}

# Rule 7: every repository carries a security policy, code owners and dependency updates
# (templates/SECURITY.md, CODEOWNERS, dependabot.yml).
function check_required_files() {
  local failed=0 file
  if [ ! -f "${REPO_ROOT}/SECURITY.md" ] && [ ! -f "${REPO_ROOT}/.github/SECURITY.md" ]; then
    log '❌ Rule 7: missing SECURITY.md (template: templates/SECURITY.md)'
    failed=1
  fi
  for file in .github/CODEOWNERS .github/dependabot.yml; do
    [ -f "${REPO_ROOT}/${file}" ] && continue
    log "❌ Rule 7: missing ${file} (template: templates/$(basename "${file}"))"
    failed=1
  done
  return "${failed}"
}

# Rule 8: targets are verb-first, <verb>-<object>[-<qualifier>], from a fixed list; single-word
# aggregates are the exceptions.
readonly TARGET_VERBS='apply|bootstrap|build|check|clean|create|deploy|detect|ensure|format|generate|help|install|lint|load|login|merge|migrate|publish|pull|push|refresh|report|run|set|setup|sign|teardown|test|update|upload|verify|wait'
readonly TARGET_AGGREGATES='audit|build|clean|format|help|lint|test'

function check_target_names() {
  local failed=0 target
  while IFS= read -r target; do
    [[ "${target}" =~ ^(${TARGET_VERBS})-[a-z0-9][a-z0-9-]*$ ]] && continue
    [[ "${target}" =~ ^(${TARGET_AGGREGATES})$ ]] && continue
    log "❌ Rule 8: target '${target}' is not verb-first (governance/ci.md, Make Targets)"
    failed=1
  done < <(awk '
      /^[A-Za-z0-9_-]+:([^=]|$)/ { if (t != "") print t; split($0, a, ":"); t = a[1]; next }
      END { if (t != "") print t }' "${EXPANDED_MAKEFILE}" | sort -u)
  return "${failed}"
}

# Rule 9: every script is named verb-first, as its target is: <verb>-<object>[-<qualifier>].sh, or one
# aggregate word. A project hook is its script's name with .local; shared libraries and git hooks are exempt.
function check_script_names() {
  local failed=0 file name
  while IFS= read -r file; do
    name="$(basename "${file}" .sh)"
    name="${name%.local}"
    [[ "${name}" =~ ^(${TARGET_VERBS})-[a-z0-9][a-z0-9-]*$ ]] && continue
    [[ "${name}" =~ ^(${TARGET_VERBS}|${TARGET_AGGREGATES})$ ]] && continue
    log "❌ Rule 9: script '${file#"${REPO_ROOT}/"}' is not named verb-first (governance/ci.md, Make Targets)"
    failed=1
  done < <(cd "${REPO_ROOT}" && find scripts -name '*.sh' -type f | grep -vE '^scripts/(lib|hooks)/' | sort -u)
  return "${failed}"
}

# Rule 10: every canonical script has a project hook: it calls run_local_hook (scripts/lib/local-hook.sh), or runs
# its own <script>.local.sh. In a consumer, the canonical scripts are those .standards/scripts holds; in
# .standards itself, every script. Shared libraries, git hooks and the hooks themselves are exempt.
function check_local_hooks() {
  local failed=0 source_root="${REPO_ROOT}" rel file
  [ -d "${REPO_ROOT}/.standards/scripts" ] && source_root="${REPO_ROOT}/.standards"
  while IFS= read -r rel; do
    file="${REPO_ROOT}/${rel}"
    [ -f "${file}" ] || continue
    grep -qE "^[^#]*(run_local_hook|$(basename "${rel}" .sh)\.local\.sh)" "${file}" && continue
    log "❌ Rule 10: canonical script '${rel}' has no project hook (run_local_hook, scripts/lib/local-hook.sh)"
    failed=1
  done < <(cd "${source_root}" && find scripts -name '*.sh' -type f ! -name '*.local.sh' | grep -vE '^scripts/(lib|hooks)/' | sort)
  return "${failed}"
}

# runtime_install_steps: "<workflow file>\t<job id>\t<step line>" for every setup step.
function runtime_install_steps() {
  local f
  for f in "${WORKFLOWS_DIR}"/*.yml "${WORKFLOWS_DIR}"/*.yaml; do
    [ -f "${f}" ] || continue
    awk -v wf=".github/workflows/$(basename "${f}")" '
      /^jobs:/ { in_jobs = 1; next }
      in_jobs && /^  [A-Za-z0-9_-]+:[[:space:]]*$/ { job = $1; sub(/:$/, "", job); next }
      in_jobs && job != "" && (/uses:[[:space:]]*actions\/setup-/ || /run:[[:space:]]*make[[:space:]]+setup-/) {
        line = $0; sub(/^[[:space:]]*-?[[:space:]]*/, "", line); print wf "\t" job "\t" line
      }' "${f}"
  done
}

# ---------------------------------------------------------------------------
function main() {
  validate_args "${@:-}"
  run_local_hook "${BASH_SOURCE[0]}" "${@:-}"
  build_expanded_makefile
  trap cleanup_expanded_makefile EXIT

  local overall=0

  check_workflow_run_steps || overall=1
  check_script_targets     || overall=1
  check_universal_targets  || overall=1
  check_recipe_shape       || overall=1
  check_runtime_installs   || overall=1
  check_required_files     || overall=1
  check_target_names       || overall=1
  check_script_names       || overall=1
  check_local_hooks        || overall=1

  if [ "${overall}" -eq 0 ]; then
    log '✅ CI audit clean: all Makefile target invariants satisfied.'
    return 0
  fi
  log ''
  log '❌ CI audit failed: findings above violate the Makefile/CI invariants.'
  log '   Run `make refresh-governance` to reconcile canonical files; resolve'
  log '   any remaining findings (e.g., new local script needs a Makefile'
  log '   target) by hand.'
  exit 1
}

main "${@:-}"
