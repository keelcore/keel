#!/usr/bin/env bash
# audit-make-targets.sh
# CI/standards compliance auditor — INFORMATIONAL. Enforces five invariants:
#   1. Every workflow run: step is `make <target>` — no direct tool calls.
#   2. Every scripts/**/*.sh (except scripts/lib/) has a Makefile target.
#   3. Universal canonical targets (build, lint, test, unit-test,
#      integration-test, clean, audit) are defined in the Makefile.
#   4. Every Makefile target has at most one active recipe line, and that
#      line invokes a single scripts/**/*.sh script. @echo / blank / # are
#      ignored; multi-step orchestration belongs in scripts/, not Makefile.
#   5. Every Makefile target's recipe-invoked scripts/**/*.sh file exists
#      on disk (no broken references; catches templates copied with stale
#      script paths).
#
# Exit semantics:
#   - This auditor is INFORMATIONAL. It exits 0 on every successful walk,
#     whether or not findings are present. Findings are printed for the
#     contributor; gating belongs to a separate target.
#     (Per .standards/governance/ci.md "Audit topology" — gating moves to
#     `make ci-governance-gate` once the governance-refactor lands.)
#   - Non-zero exit is reserved for HARD errors: malformed args, unreadable
#     Makefile — not for rule violations. A missing .github/workflows/
#     directory is NOT a hard error: consumers may use a different CI
#     provider or not yet have CI wired up; Rule 1 simply skips in that case.
# Safe to run locally: make audit

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

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
  integration-test
  lint
  test
  unit-test
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
  # scripts (not shipped to consumers — e.g. governance-refresh.sh) are
  # invoked from the consumer Makefile via the submodule path.
  # Env-var prefix forms accepted (each may repeat):
  #   - VAR=unquoted_value
  #   - VAR='single quoted value with spaces'
  #   - VAR="double quoted value with spaces"
  stripped="${cmd}"
  while [[ "${stripped}" =~ ^[A-Z_][A-Z0-9_]*=(\'[^\']*\'|\"[^\"]*\"|[^[:space:]\'\"]+)[[:space:]]+ ]]; do
    stripped="${stripped#"${BASH_REMATCH[0]}"}"
  done

  if [[ "${stripped}" =~ ^(bash[[:space:]]+|sh[[:space:]]+|\.?/?)((\.standards/)?scripts/[^[:space:]]+\.sh) ]]; then
    # Rule 4 satisfied. Now Rule 5: does the referenced script exist on disk?
    # The canonical Makefile (templates/Makefile.canonical) only references
    # scripts that ARE shipped to consumers by governance-refresh, so a
    # broken Rule 5 finding always points to a real bug — either consumer
    # drift or a missing canonical script — never to a known-standards-only
    # script masquerading as consumer-facing.
    local script_path="${BASH_REMATCH[2]}"
    if [ ! -f "${REPO_ROOT}/${script_path}" ]; then
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
function main() {
  validate_args "${@:-}"
  build_expanded_makefile
  trap cleanup_expanded_makefile EXIT

  local overall=0

  check_workflow_run_steps || overall=1
  check_script_targets     || overall=1
  check_universal_targets  || overall=1
  check_recipe_shape       || overall=1

  if [ "${overall}" -eq 0 ]; then
    log '✅ CI audit clean: all Makefile target invariants satisfied.'
  else
    log ''
    log 'ℹ️  Audit findings above are INFORMATIONAL — the audit does not gate.'
    log '   Run `make governance-refresh` to reconcile canonical files; resolve'
    log '   any remaining findings (e.g., new local script needs a Makefile'
    log '   target) by hand. Gating is enforced separately by `make ci-governance-gate`.'
  fi
  # Audit always exits 0 on a successful walk; hard errors (bad args,
  # unreadable Makefile) exit non-zero via validate_args / set -o errexit.
}

main "${@:-}"
