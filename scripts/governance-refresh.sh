#!/usr/bin/env bash
# governance-refresh.sh
# Reconciles a consumer repo with its `.standards` canonical source of truth.
#
# Two automatic reconciliation passes plus a drift-detection step. `.standards`
# wins on artifacts the consumer is never expected to hand-edit; consumer
# overrides to Makefile recipes are preserved (substantive divergence is
# legitimate customization, not drift to clobber).
#
#   1. Canonical-script sync: walks .standards/scripts/ (excluding standards-
#      only paths: bootstrap-standards.sh, release/, verify/), copies any
#      missing or content-divergent script into the consumer's matching path.
#      Canonical wins.
#
#   2. Makefile target injection: only when the consumer Makefile has NO
#      `include .standards/templates/Makefile.canonical` directive (i.e.,
#      legacy/unmigrated layouts that ensure_consumer_migrated couldn't
#      convert). For each canonical script lacking a consumer target, lift
#      the canonical block in. With the include directive present, the
#      consumer inherits every canonical target — injection would only
#      duplicate them and is skipped.
#
#   3. Makefile target drift DETECTION (no auto-fix): for each target in
#      BOTH the canonical template and the consumer Makefile whose active
#      recipe lines differ — comparing the SUBSTANTIVE command lines only,
#      after stripping echoes/comments/decoration — REPORT the divergence
#      and exit non-zero. The override is preserved verbatim; the operator
#      decides whether to align with canonical or keep the customization.
#
# Idempotent on passes 1 and 2: re-running converges; second run produces
# no diff. Pass 3 stays non-zero until the operator reconciles.
#
# Usage:
#   bash governance-refresh.sh [--dry-run]
#
#   --dry-run : compute and report pending changes; touch no files; exit 1
#               if any change is pending, exit 0 if nothing pending.
#
# Env overrides (testing only — production reads paths from git):
#   GOVREFRESH_STANDARDS_ROOT  defaults to ${REPO_ROOT}/.standards
#   GOVREFRESH_REPO_ROOT       defaults to `git rev-parse --show-toplevel`

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

declare REPO_ROOT
REPO_ROOT="${GOVREFRESH_REPO_ROOT:-$(git rev-parse --show-toplevel)}"
declare -r REPO_ROOT

declare STANDARDS_ROOT
STANDARDS_ROOT="${GOVREFRESH_STANDARDS_ROOT:-${REPO_ROOT}/.standards}"
declare -r STANDARDS_ROOT

declare -r TEMPLATES_MAKEFILE="${STANDARDS_ROOT}/templates/Makefile.canonical"
declare -r CONSUMER_MAKEFILE="${REPO_ROOT}/Makefile"

declare DRY_RUN=0
# Set by detect_consumer:
#   IS_CONSUMER  — STANDARDS_ROOT is a directory nested under REPO_ROOT
#                  (the consumer-repo layout, regardless of whether it
#                  came from a submodule or a symlink/test harness).
#   IS_SUBMODULE — `.standards` is a real git submodule (narrower; needed
#                  for the git-pull / git-stage operations).
declare IS_CONSUMER=0
declare IS_SUBMODULE=0

# Absolute path of THIS running script, captured before any re-exec. Used
# by reexec_from_canonical_if_stale to detect whether `make governance-
# refresh` invoked the consumer's local (potentially stale) copy at
# scripts/governance-refresh.sh instead of the canonical copy at
# .standards/scripts/governance-refresh.sh.
declare SELF_PATH
SELF_PATH="$(realpath -- "${BASH_SOURCE[0]}" 2>/dev/null || echo "${BASH_SOURCE[0]}")"
declare -r SELF_PATH

function main() {
  exec 5>&1
  parse_args "${@:-}"
  detect_consumer
  validate_env
  maybe_pull_submodule
  reexec_from_canonical_if_stale "${@:-}"
  ensure_consumer_migrated
  log "🔄 governance-refresh: REPO_ROOT=${REPO_ROOT}"

  # ── Phase 1: PLUMBING ────────────────────────────────────────────────
  # Sync canonical scripts and inject any missing canonical Makefile
  # targets so the consumer has a complete, working install BEFORE we
  # look at recipe-level drift. A "half-assed install" (scripts present
  # but Makefile.canonical absent, or vice versa) would make manual
  # repair of drift impossible.
  local script_changes target_injections
  script_changes="$(compute_script_changes)"
  target_injections="$(compute_target_injections)"
  report_plumbing_summary "${script_changes}" "${target_injections}"

  if [ "${DRY_RUN}" -eq 1 ]; then
    dry_run_exit "${script_changes}" "${target_injections}"
    return 0
  fi

  apply_script_changes "${script_changes}"
  apply_target_injections "${target_injections}"
  maybe_stage_submodule
  log "✅ Plumbing in place (canonical scripts synced; Makefile.canonical include present)"

  # ── Phase 2: DRIFT DETECTION ─────────────────────────────────────────
  # Recompute drift against the post-plumbing state. Substantive
  # divergence in consumer overrides is preserved as-is and reported
  # for manual repair; exit non-zero so CI gates surface it.
  local target_drifts
  target_drifts="$(compute_target_drifts)"
  report_drifts_for_manual_repair "${target_drifts}"
  log "✅ governance-refresh complete"
}

function parse_args() {
  local arg
  for arg in "${@:-}"; do
    case "${arg:-}" in
      ''|--) ;;
      --dry-run) DRY_RUN=1 ;;
      *)
        log "❌ unknown argument: ${arg}"
        exit 2
        ;;
    esac
  done
}

function validate_env() {
  if [ ! -d "${STANDARDS_ROOT}/scripts" ]; then
    if [ "${IS_SUBMODULE}" -eq 1 ]; then
      log "❌ .standards submodule not initialized at ${STANDARDS_ROOT}"
      log '   Run: git submodule update --init --recursive'
      exit 2
    fi
    log 'ℹ️  governance-refresh: no .standards submodule (running in the standards repo itself); nothing to refresh.'
    exit 0
  fi
  if [ ! -f "${TEMPLATES_MAKEFILE}" ]; then
    log "❌ STANDARDS_ROOT missing templates/Makefile.canonical: ${TEMPLATES_MAKEFILE}"
    exit 2
  fi
  if [ ! -f "${CONSUMER_MAKEFILE}" ]; then
    log "❌ REPO_ROOT missing Makefile: ${CONSUMER_MAKEFILE}"
    exit 2
  fi
}

# Distinguishes (1) "are we consumer-shaped" — STANDARDS_ROOT is a real
# directory nested under REPO_ROOT — from (2) "is .standards a real git
# submodule" — the narrower predicate needed by the git-pull / git-stage
# operations. The standards repo itself has neither and stays at 0/0.
function detect_consumer() {
  case "${STANDARDS_ROOT}" in
    "${REPO_ROOT}/"*)
      if [ -d "${STANDARDS_ROOT}" ] && [ "${STANDARDS_ROOT}" != "${REPO_ROOT}" ]; then
        IS_CONSUMER=1
      fi
      ;;
  esac
  if git -C "${REPO_ROOT}" submodule status .standards 2>/dev/null \
      | grep -q .; then
    IS_SUBMODULE=1
  fi
}

# If the consumer Makefile predates the canonical-include era (no
# `include .standards/templates/Makefile.canonical` directive), auto-run
# migrate-makefile.sh before continuing the refresh — governance-refresh
# is the one-stop reconciliation entry point; the operator should never
# have to chain it with a manual prerequisite.
#
# Dry-run mode does NOT migrate: it reports the pending migration and
# exits 1 so the CI governance gate fails until 'make governance-refresh'
# is run for real.
#
# Scope: fires only when STANDARDS_ROOT is a directory NESTED inside
# REPO_ROOT (the consumer-repo layout). The standards repo itself, and
# test harnesses that point STANDARDS_ROOT at an out-of-tree path, are
# exempt.
function ensure_consumer_migrated() {
  [ "${STANDARDS_ROOT}" = "${REPO_ROOT}" ] && return 0
  case "${STANDARDS_ROOT}" in
    "${REPO_ROOT}/"*) ;;
    *) return 0 ;;
  esac
  if grep -qE '^[[:space:]]*-?include[[:space:]]+\.standards/(templates/Makefile\.canonical|Makefile)\b' \
      "${CONSUMER_MAKEFILE}"; then
    return 0
  fi
  if [ "${DRY_RUN}" -eq 1 ]; then
    log "🛠  Consumer Makefile is unmigrated — would invoke migrate-makefile.sh"
    log "⚠️  dry-run: migration pending. Run 'make governance-refresh' to apply."
    exit 1
  fi
  log "🛠  Consumer Makefile is unmigrated — invoking migrate-makefile.sh"
  MIGRATE_REPO_ROOT="${REPO_ROOT}" \
    bash "${STANDARDS_ROOT}/scripts/migrate-makefile.sh"
}

# Advance the `.standards` submodule to its tracked-branch tip. Skipped in
# dry-run mode: the CI governance gate runs --dry-run and must evaluate
# against the pinned submodule pointer, not whatever upstream looks like
# right now — otherwise the gate becomes flaky. Also skipped after a
# re-exec (already pulled in the parent invocation).
function maybe_pull_submodule() {
  [ "${GOVREFRESH_REEXEC:-0}" = "1" ] && return 0
  [ "${IS_SUBMODULE}" -eq 1 ] || return 0
  [ "${DRY_RUN}" -eq 1 ] && return 0
  log "📡 Pulling latest .standards submodule..."
  git -C "${REPO_ROOT}" submodule update --remote .standards
}

# When `make governance-refresh` invokes the consumer's LOCAL copy at
# scripts/governance-refresh.sh, that copy may be older than the canonical
# at .standards/scripts/governance-refresh.sh — the running process was
# loaded into bash memory before maybe_pull_submodule fetched the fresh
# canonical, so it would otherwise execute stale logic for the remainder
# of the run (and self-update only on disk, helping the NEXT invocation
# instead of this one). Re-exec into the canonical so the current run uses
# the latest code.
#
# Loop guard: `GOVREFRESH_REEXEC=1` is exported before exec so the child
# process skips this check (and skips maybe_pull_submodule, since the
# parent already pulled).
#
# Skipped when already running the canonical (realpath match), when the
# canonical is content-identical to self (sha match), when not a consumer
# (no .standards submodule), or when the canonical is missing.
function reexec_from_canonical_if_stale() {
  [ "${GOVREFRESH_REEXEC:-0}" = "1" ] && return 0
  [ "${IS_CONSUMER}" -eq 1 ] || return 0
  local canonical="${STANDARDS_ROOT}/scripts/governance-refresh.sh"
  [ -f "${canonical}" ] || return 0
  local canonical_real
  canonical_real="$(realpath -- "${canonical}" 2>/dev/null || echo "${canonical}")"
  [ "${SELF_PATH}" = "${canonical_real}" ] && return 0
  local self_sum canonical_sum
  self_sum="$(sha256sum "${SELF_PATH}" | awk '{print $1}')"
  canonical_sum="$(sha256sum "${canonical_real}" | awk '{print $1}')"
  [ "${self_sum}" = "${canonical_sum}" ] && return 0
  log "🔁 Local scripts/governance-refresh.sh differs from canonical; re-exec"
  log "   from ${canonical_real}"
  export GOVREFRESH_REEXEC=1
  exec bash "${canonical_real}" "${@}"
}

# Stage the (possibly advanced) submodule pointer so the consumer's index
# is ready to commit. No-op if the pointer did not move.
function maybe_stage_submodule() {
  [ "${IS_SUBMODULE}" -eq 1 ] || return 0
  [ "${DRY_RUN}" -eq 1 ] && return 0
  log "📌 Staging .standards submodule pointer..."
  git -C "${REPO_ROOT}" add .standards
}

# List shipping canonical scripts as paths relative to ${STANDARDS_ROOT}.
# Excludes standards-only paths. The refresh script itself IS shipped so it
# can self-update on subsequent runs.
#   - Top-level standards-only files (exact match): bootstrap-standards.sh
#     (consumers `curl` it once; they do not keep a local copy).
#   - Standards-only directory trees (prefix match): scripts/release/,
#     scripts/verify/.
function canonical_scripts() {
  find "${STANDARDS_ROOT}/scripts" -name '*.sh' -type f \
    | sed "s|^${STANDARDS_ROOT}/||" \
    | grep -v -E '^scripts/bootstrap-standards\.sh$' \
    | grep -v -E '^scripts/(release|verify)/' \
    | sort
}

# Emit pending script changes as lines of the form:
#   NEW <rel-path>
#   MOD <rel-path>
# (no output when consumer already matches .standards)
function compute_script_changes() {
  local rel src_sum dst_sum
  while IFS= read -r rel; do
    [ -z "${rel}" ] && continue
    if [ ! -f "${REPO_ROOT}/${rel}" ]; then
      printf 'NEW %s\n' "${rel}"
      continue
    fi
    src_sum="$(sha256sum "${STANDARDS_ROOT}/${rel}" | awk '{print $1}')"
    dst_sum="$(sha256sum "${REPO_ROOT}/${rel}" | awk '{print $1}')"
    if [ "${src_sum}" != "${dst_sum}" ]; then
      printf 'MOD %s\n' "${rel}"
    fi
  done < <(canonical_scripts)
}

# Emit pending target injections as lines of the form:
#   INJECT <target_name> <script_rel_path>
# For each canonical script in consumer/scripts/ that is NOT invoked by any
# target in consumer/Makefile, look up the target in Makefile.canonical
# that invokes it; emit if a canonical target is defined.
#
# Short-circuit when the consumer Makefile already has the canonical-include
# directive: the included file provides every canonical target, so injecting
# them into the consumer body would only duplicate them. Worse, if the
# consumer's drifted recipe happens to invoke the script via the
# `.standards/scripts/` path instead of `scripts/`, `consumer_invokes_script`
# would miss it and pass 2 would emit a duplicate target — `active_recipe_of`
# then concatenates both bodies and the drift becomes self-perpetuating.
function compute_target_injections() {
  if has_include_directive; then
    return 0
  fi
  local rel target
  while IFS= read -r rel; do
    [ -z "${rel}" ] && continue
    target="$(find_target_for_script "${rel}")"
    [ -z "${target}" ] && continue
    if consumer_provides_target "${target}" "${rel}"; then
      continue
    fi
    printf 'INJECT %s %s\n' "${target}" "${rel}"
  done < <(canonical_scripts)
}

# True iff CONSUMER_MAKEFILE contains an `include` directive pointing at the
# canonical template (current or legacy form). Mirrors migrate-makefile.sh's
# idempotency check.
function has_include_directive() {
  grep -qE '^[[:space:]]*-?include[[:space:]]+\.standards/(templates/Makefile\.canonical|Makefile)\b' \
    "${CONSUMER_MAKEFILE}"
}

# True iff the consumer Makefile already provides the named canonical target.
# Defines "provides" in the correct, name-based sense — the consumer has a
# target line `^<name>[[:space:]]*:`. The old content-based check (grep for
# `bash <rel-path>`) missed consumer overrides whose recipe diverged from
# canonical (e.g. `ln -sf ...` instead of `bash scripts/install-hooks.sh`),
# causing pass 2 to inject a duplicate of an already-present target.
#
# A secondary content-based fallback catches consumers that invoke the
# canonical script from a differently-named target's recipe (without
# defining the canonical target name themselves) — rare but legitimate.
function consumer_provides_target() {
  local -r target="${1}"
  local -r rel="${2}"
  if grep -qE "^${target}[[:space:]]*:" "${CONSUMER_MAKEFILE}"; then
    return 0
  fi
  grep -qF "bash ${rel}" "${CONSUMER_MAKEFILE}"
}

# Emit pending target drifts as lines of the form:
#   DRIFT <target_name>
# For each target name defined in BOTH Makefile.canonical and consumer
# Makefile, compare the active recipe lines (ignoring blank, comment, and
# @echo lines per Rule 4's "active" definition). Normalize `.standards/`
# prefix so cross-context invocations of standards-only scripts (e.g.
# governance-refresh) don't false-positive.
function compute_target_drifts() {
  local target t_active c_active t_norm c_norm
  while IFS= read -r target; do
    [ -z "${target}" ] && continue
    # Skip targets unique to one Makefile — those are caught by Rule 2
    # (consumer-only) or the injection pass (templates-only).
    if ! grep -qE "^${target}[[:space:]]*:" "${CONSUMER_MAKEFILE}"; then
      continue
    fi
    t_active="$(active_recipe_of "${TEMPLATES_MAKEFILE}" "${target}")"
    c_active="$(active_recipe_of "${CONSUMER_MAKEFILE}" "${target}")"
    # Templates side empty: provides a stub for the consumer to fill in
    # (e.g. `build:` in the docs-only standards repo where the consumer
    # supplies a real `bash scripts/build.sh`). Expected customization.
    if [ -z "${t_active}" ]; then
      continue
    fi
    # Consumer side empty after decoration stripping. Two distinct cases:
    #   (a) pure aggregator — `target: dep1 dep2` with NO tab-indented
    #       body whatsoever. Legitimately delegates to prerequisites; not
    #       drift.
    #   (b) `@echo`-only no-op override — consumer wrote a tab-indented
    #       body that announces something but doesn't actually run the
    #       canonical command. Make sees this as a recipe and emits
    #       `warning: overriding commands for target` against the
    #       canonical recipe inherited via include. THIS IS DRIFT.
    if [ -z "${c_active}" ]; then
      if target_has_recipe_body "${CONSUMER_MAKEFILE}" "${target}"; then
        printf 'DRIFT %s\n' "${target}"
      fi
      continue
    fi
    t_norm="$(normalize_recipe "${t_active}")"
    c_norm="$(normalize_recipe "${c_active}")"
    if [ "${t_norm}" != "${c_norm}" ]; then
      printf 'DRIFT %s\n' "${target}"
    fi
  done < <(template_target_names)
}

# True iff the consumer Makefile has at least one tab-indented body line
# under the named target — distinguishes a deliberate `@echo`-only no-op
# override (has body, body is all decoration) from a pure aggregator
# (`target: dep1 dep2` with NO body at all). Only the former is drift;
# the latter is a legitimate use of Make's prerequisite mechanism.
function target_has_recipe_body() {
  local -r makefile="${1}"
  local -r target="${2}"
  awk -v t="^${target}[[:space:]]*:" '
    $0 ~ t { in_block=1; next }
    /^[a-zA-Z_]/ { in_block=0 }
    in_block && /^\t/ { found=1 }
    END { exit !found }
  ' "${makefile}"
}

# Names of all targets defined in Makefile.canonical.
function template_target_names() {
  awk '
    /^[a-zA-Z_][a-zA-Z0-9_.-]*[[:space:]]*:/ &&
      !/^[a-zA-Z_][a-zA-Z0-9_.-]*[[:space:]]*[?:+]?=/ {
        sub(/[[:space:]]*:.*$/, "")
        print
    }
  ' "${TEMPLATES_MAKEFILE}" | sort -u
}

# Active recipe lines for the named target — the substantive command lines,
# stripped of decoration so drift detection compares "what gets run", not
# how it's announced. For each TAB-indented body line:
#   * strip leading TABs and recipe modifiers (`@`, `-`)
#   * strip a trailing inline `# comment` and any trailing whitespace
#   * drop empty lines, full-line `#` comments, and `@?echo` announcements
# Per Rule 4's "active recipe" definition.
function active_recipe_of() {
  local -r makefile="${1}"
  local -r target="${2}"
  awk -v t="^${target}[[:space:]]*:" '
    $0 ~ t { in_block=1; next }
    /^[a-zA-Z_]/ { in_block=0 }
    in_block && /^\t/ {
      body=$0
      sub(/^\t+/, "", body)
      sub(/^[@-]+/, "", body)
      sub(/[[:space:]]+#.*$/, "", body)
      sub(/[[:space:]]+$/, "", body)
      if (body == "") next
      if (body ~ /^#/) next
      if (body ~ /^echo([[:space:]]|$)/) next
      print body
    }
  ' "${makefile}"
}

# Normalize a recipe for drift comparison:
#   - collapse multiple whitespace to single space
#   - strip an optional `.standards/` prefix on a scripts/ path so
#     cross-context invocations (consumer Makefile invokes the .standards
#     submodule path; templates invokes the script via the relative path)
#     compare equal.
function normalize_recipe() {
  local -r raw="${1}"
  printf '%s\n' "${raw}" \
    | sed -E 's| \.standards/scripts/| scripts/|g; s|[[:space:]]+| |g'
}

# Find the target in Makefile.canonical whose recipe invokes the given
# script. Returns empty if no canonical target invokes it.
function find_target_for_script() {
  local -r rel="${1}"
  local lineno
  lineno="$(grep -nF "bash ${rel}" "${TEMPLATES_MAKEFILE}" | head -1 | cut -d: -f1)"
  if [ -z "${lineno}" ]; then
    return 0
  fi
  awk -v ln="${lineno}" '
    NR<=ln && /^[a-zA-Z_][a-zA-Z0-9_.-]*[[:space:]]*:/ { last=$0 }
    END { if (last) { sub(/[[:space:]]*:.*$/, "", last); print last } }
  ' "${TEMPLATES_MAKEFILE}"
}

function report_plumbing_summary() {
  local -r script_changes="${1}"
  local -r target_injections="${2}"
  if [ -z "${script_changes}" ] && [ -z "${target_injections}" ]; then
    log "  plumbing: nothing to sync (canonical scripts and Makefile.canonical include up to date)"
    return 0
  fi
  if [ -n "${script_changes}" ]; then
    log "  canonical scripts to sync:"
    while IFS= read -r line; do
      [ -n "${line}" ] && log "    ${line}"
    done <<< "${script_changes}"
  fi
  if [ -n "${target_injections}" ]; then
    log "  Makefile targets to inject (only for legacy/unmigrated layouts):"
    while IFS= read -r line; do
      [ -n "${line}" ] && log "    ${line}"
    done <<< "${target_injections}"
  fi
}

# Dry-run terminator. Reports any pending plumbing AND any drift (since
# dry-run can't actually apply the plumbing, drift is computed against
# the as-is state — best-effort, but the gate still surfaces it).
function dry_run_exit() {
  local -r script_changes="${1}"
  local -r target_injections="${2}"
  local target_drifts
  target_drifts="$(compute_target_drifts)"
  local pending=0
  if [ -n "${script_changes}" ] || [ -n "${target_injections}" ]; then
    pending=1
  fi
  if [ -n "${target_drifts}" ]; then
    log "  Makefile target drift (consumer override diverges substantively"
    log "  from .standards/templates/Makefile.canonical — manual repair):"
    while IFS= read -r line; do
      [ -n "${line}" ] && log "    ${line}"
    done <<< "${target_drifts}"
    pending=1
  fi
  if [ "${pending}" -eq 1 ]; then
    log "⚠️  dry-run: changes pending. Run 'make governance-refresh' to apply"
    log "    plumbing; substantive recipe drift requires MANUAL repair."
    exit 1
  fi
  log "✅ dry-run: nothing to refresh"
}

function apply_script_changes() {
  local -r changes="${1}"
  [ -z "${changes}" ] && return 0
  local kind rel dst_dir
  while IFS=' ' read -r kind rel; do
    [ -z "${rel}" ] && continue
    case "${kind}" in
      NEW|MOD) ;;
      *)
        log "❌ apply_script_changes: unexpected kind '${kind}' for ${rel}"
        exit 3
        ;;
    esac
    dst_dir="${REPO_ROOT}/$(dirname "${rel}")"
    mkdir -p "${dst_dir}"
    cp "${STANDARDS_ROOT}/${rel}" "${REPO_ROOT}/${rel}"
    chmod +x "${REPO_ROOT}/${rel}"
  done <<< "${changes}"
}

function apply_target_injections() {
  local -r injections="${1}"
  [ -z "${injections}" ] && return 0
  {
    printf '\n## --- governance-refresh injected from .standards/templates/Makefile.canonical ---\n'
    local kind target rel
    while IFS=' ' read -r kind target rel; do
      [ -z "${target}" ] && continue
      if [ "${kind}" != "INJECT" ]; then
        log "❌ apply_target_injections: unexpected kind '${kind}' for ${target}"
        exit 3
      fi
      printf '\n# Injected because canonical script %s has no consumer target.\n' "${rel}"
      extract_target_block "${target}"
    done <<< "${injections}"
  } >> "${CONSUMER_MAKEFILE}"
}

# Print the target's block from Makefile.canonical: from `^target:` through
# the line before the next blank line. Recipe lines are TAB-indented per Make.
function extract_target_block() {
  local -r target="${1}"
  awk -v t="^${target}[[:space:]]*:" '
    $0 ~ t { in_block=1 }
    in_block && /^$/ { in_block=0; next }
    in_block { print }
  ' "${TEMPLATES_MAKEFILE}"
}

# Report substantive drift between consumer overrides and canonical recipes.
# Semantic drift only — active_recipe_of strips human-readable text (echoes,
# comments, decoration) before comparison, so what's reported here is a real
# command-level divergence. The consumer's override is preserved verbatim;
# resolution is up to the operator (align with canonical, delete the override
# to let `include .standards/templates/Makefile.canonical` provide it, or
# leave the divergence intentional and accept the exit-non-zero from this
# function on future runs until reconciled).
#
# Exits 5 if any drift exists (so CI gates surface it). Pass 1 (canonical
# scripts) and the migration have already run by the time we get here, so
# the consumer has the supporting infrastructure regardless.
function report_drifts_for_manual_repair() {
  local -r drifts="${1}"
  [ -z "${drifts}" ] && return 0
  log ""
  log "⚠️  Substantive Makefile target drift — MANUAL REPAIR required."
  log "   Canonical scripts and templates/Makefile.canonical are installed;"
  log "   the consumer's overrides below diverge from canonical at the command"
  log "   level and are preserved as-is. Reconcile by either editing the"
  log "   consumer override to match canonical, or deleting the override to"
  log "   inherit the canonical recipe via the include directive."
  local kind target c_active t_active count
  while IFS=' ' read -r kind target; do
    [ -z "${target}" ] && continue
    log ""
    log "   --- ${target} ---"
    count="$(grep -cE "^${target}[[:space:]]*:" "${CONSUMER_MAKEFILE}" || true)"
    log "   target definitions in consumer Makefile: ${count} (canonical has 1)"
    t_active="$(active_recipe_of "${TEMPLATES_MAKEFILE}" "${target}")"
    c_active="$(active_recipe_of "${CONSUMER_MAKEFILE}" "${target}")"
    log "   canonical active recipe:"
    while IFS= read -r line; do log "     | ${line}"; done <<< "${t_active}"
    log "   consumer active recipe:"
    while IFS= read -r line; do log "     | ${line}"; done <<< "${c_active}"
  done <<< "${drifts}"
  exit 5
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/governance_refresh.log' >&5
}

main "${@:-}"
