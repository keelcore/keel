#!/usr/bin/env bash
# migrate-makefile.sh
# One-shot migration for consumer repos whose Makefile is a clone of the
# pre-include-era templates/Makefile (or the older `.standards/Makefile`
# include form). Converts it into the thin pattern:
#
#   - prepends the existence guard for .standards/templates/Makefile.canonical
#   - prepends `include .standards/templates/Makefile.canonical`
#   - removes every target block whose recipe matches the canonical recipe
#     in .standards/templates/Makefile.canonical (safe deletion: behavior
#     unchanged after the include picks it up)
#
# Targets whose recipes DIFFER from canonical (drift) are left alone and
# reported for manual review. The original Makefile is backed up to
# Makefile.pre-migrate.bak before any rewrite.
#
# Idempotent: if the Makefile already contains an
# `include .standards/templates/Makefile.canonical` directive (or the legacy
# `include .standards/Makefile` form), the script exits 0 with no changes.
#
# Usage:
#   bash scripts/migrate-makefile.sh [--dry-run]
#
#   --dry-run : report what would be removed and what drifts; do not
#               touch any file; exit 0 on success.
#
# Env overrides (testing only):
#   MIGRATE_REPO_ROOT  defaults to `git rev-parse --show-toplevel`

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

declare REPO_ROOT
REPO_ROOT="${MIGRATE_REPO_ROOT:-$(git rev-parse --show-toplevel)}"
declare -r REPO_ROOT

declare -r CONSUMER_MAKEFILE="${REPO_ROOT}/Makefile"
declare -r CANONICAL_MAKEFILE="${REPO_ROOT}/.standards/templates/Makefile.canonical"
declare -r BACKUP_PATH="${REPO_ROOT}/Makefile.pre-migrate.bak"

declare DRY_RUN=0

function main() {
  exec 5>&1
  parse_args "${@:-}"
  validate_env
  if has_include_directive; then
    log '✅ Already migrated (include directive present); no action.'
    return 0
  fi
  validate_backup_path
  local removable drift
  removable="$(compute_removable)"
  drift="$(compute_drift)"
  report "${removable}" "${drift}"
  if [ "${DRY_RUN}" -eq 1 ]; then
    log 'ℹ️  --dry-run: no changes written.'
    return 0
  fi
  backup_makefile
  rewrite_makefile "${removable}"
  log "✅ Migration complete. Backup at ${BACKUP_PATH}"
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/migrate_makefile.log' >&5
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
  if [ ! -f "${CONSUMER_MAKEFILE}" ]; then
    log "❌ Consumer Makefile not found: ${CONSUMER_MAKEFILE}"
    exit 2
  fi
  if [ ! -f "${CANONICAL_MAKEFILE}" ]; then
    log "❌ Canonical Makefile not found: ${CANONICAL_MAKEFILE}"
    log '   Run: git submodule update --init --recursive'
    log '   (Older standards versions exposed canonical at .standards/Makefile —'
    log '   bump the submodule to a version that ships templates/Makefile.canonical.)'
    exit 2
  fi
}

function validate_backup_path() {
  [ "${DRY_RUN}" -eq 1 ] && return 0
  if [ -e "${BACKUP_PATH}" ]; then
    log "❌ Backup path already exists: ${BACKUP_PATH}"
    log '   Move or delete it before re-running the migration.'
    exit 2
  fi
}

function has_include_directive() {
  # Accept both the current canonical path and the legacy form so the
  # idempotency check still fires on partially migrated repos.
  grep -qE '^[[:space:]]*-?include[[:space:]]+\.standards/(templates/Makefile\.canonical|Makefile)\b' \
    "${CONSUMER_MAKEFILE}"
}

# Target names defined in a Makefile (excludes variable assignments and
# target-specific variable settings).
function target_names_in() {
  local -r makefile="${1}"
  awk '
    /^[a-zA-Z_][a-zA-Z0-9_.-]*[[:space:]]*:/ &&
    !/^[a-zA-Z_][a-zA-Z0-9_.-]*[[:space:]]*[?:+]?=/ {
      sub(/[[:space:]]*:.*$/, "")
      print
    }
  ' "${makefile}" | sort -u
}

# Active recipe lines for the named target: TAB-indented body lines that
# are not blank, comment, or @echo (Rule 4 "active" definition). Strips
# leading @ / - modifiers.
function active_recipe_of() {
  local -r makefile="${1}"
  local -r target="${2}"
  awk -v t="^${target}[[:space:]]*:" '
    $0 ~ t { in_block=1; next }
    /^[a-zA-Z]/ { in_block=0 }
    in_block && /^\t/ {
      body=$0
      sub(/^\t+/, "", body)
      if (body == "") next
      if (body ~ /^#/) next
      if (body ~ /^@?-?echo([[:space:]]|$)/) next
      sub(/^[@-]+/, "", body)
      print body
    }
  ' "${makefile}"
}

# Normalize a recipe for comparison: collapse whitespace runs to a single
# space; strip an optional `.standards/` prefix on `scripts/` paths so
# cross-context invocations compare equal.
function normalize_recipe() {
  local -r raw="${1}"
  printf '%s\n' "${raw}" \
    | sed -E 's| \.standards/scripts/| scripts/|g; s|[[:space:]]+| |g'
}

# Emit each target name (one per line) whose normalized active recipe is
# identical between consumer and canonical — safe to remove.
function compute_removable() {
  local target c_recipe k_recipe
  while IFS= read -r target; do
    [ -z "${target}" ] && continue
    target_in_canonical "${target}" || continue
    c_recipe="$(normalize_recipe "$(active_recipe_of "${CONSUMER_MAKEFILE}" "${target}")")"
    k_recipe="$(normalize_recipe "$(active_recipe_of "${CANONICAL_MAKEFILE}" "${target}")")"
    if [ "${c_recipe}" = "${k_recipe}" ]; then
      printf '%s\n' "${target}"
    fi
  done < <(target_names_in "${CONSUMER_MAKEFILE}")
}

# Emit each target name whose active recipe DIFFERS substantively between
# consumer and canonical — drift requiring manual review. Includes the
# `@echo`-only no-op override case: a consumer whose target body strips
# down to nothing after decoration removal but DOES have a tab-indented
# body still shadows the canonical recipe at Make time and elicits a
# `warning: overriding commands for target` from Make. That's drift, not
# a pure aggregator.
function compute_drift() {
  local target c_recipe k_recipe
  while IFS= read -r target; do
    [ -z "${target}" ] && continue
    target_in_canonical "${target}" || continue
    c_recipe="$(normalize_recipe "$(active_recipe_of "${CONSUMER_MAKEFILE}" "${target}")")"
    k_recipe="$(normalize_recipe "$(active_recipe_of "${CANONICAL_MAKEFILE}" "${target}")")"
    # Canonical stub: expected customization.
    if [ -z "${k_recipe}" ]; then
      continue
    fi
    # Consumer side empty post-strip — drift only if there's a tab-
    # indented body (deliberate no-op override), not for pure aggregators.
    if [ -z "${c_recipe}" ]; then
      if target_has_recipe_body "${CONSUMER_MAKEFILE}" "${target}"; then
        printf '%s\n' "${target}"
      fi
      continue
    fi
    if [ "${c_recipe}" != "${k_recipe}" ]; then
      printf '%s\n' "${target}"
    fi
  done < <(target_names_in "${CONSUMER_MAKEFILE}")
}

# True iff the named target in `makefile` has at least one tab-indented
# body line — distinguishes a deliberate `@echo`-only no-op override
# (has body, body is all decoration) from a pure aggregator
# (`target: dep1 dep2` with NO body at all).
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

function target_in_canonical() {
  local -r target="${1}"
  grep -qE "^${target}[[:space:]]*:" "${CANONICAL_MAKEFILE}"
}

function report() {
  local -r removable="${1}"
  local -r drift="${2}"
  log '🔍 Migration report:'
  report_block 'Removable (recipes match canonical)' "${removable}"
  if [ -n "${drift}" ]; then
    report_block '⚠️  Drift (recipes differ — left in place for manual review)' "${drift}"
  fi
}

function report_block() {
  local -r heading="${1}"
  local -r body="${2}"
  log "  ${heading}:"
  if [ -z "${body}" ]; then
    log '    (none)'
    return 0
  fi
  local line
  while IFS= read -r line; do
    [ -n "${line}" ] && log "    - ${line}"
  done <<< "${body}"
}

function backup_makefile() {
  cp "${CONSUMER_MAKEFILE}" "${BACKUP_PATH}"
  log "📦 Backup: ${BACKUP_PATH}"
}

function rewrite_makefile() {
  local -r removable="${1}"
  local tmp
  tmp="$(mktemp -t migrate-makefile.XXXXXX)"
  write_header > "${tmp}"
  write_remainder "${removable}" | strip_orphan_comments >> "${tmp}"
  mv "${tmp}" "${CONSUMER_MAKEFILE}"
}

# Remove orphan comment blocks: column-0 comment groups (possibly separated
# by blank lines) that are NOT followed by a real statement (target,
# .PHONY, directive, etc.) before the next comment block or end-of-file.
# A comment block's "real next line" is the first non-comment, non-blank
# line after it; if that's another comment, the prior block is orphan.
# Also collapses consecutive blank lines to a single blank.
function strip_orphan_comments() {
  awk '
    { L[++N] = $0 }
    END {
      for (i = 1; i <= N; i++) {
        if (L[i] ~ /^#/)      T[i] = "C"
        else if (L[i] == "")  T[i] = "B"
        else                  T[i] = "R"
        keep[i] = 1
      }
      i = 1
      while (i <= N) {
        if (T[i] != "C") { i++; continue }
        j = i
        while (j <= N && T[j] == "C") j++
        k = j
        while (k <= N && T[k] == "B") k++
        if (k > N || T[k] != "R") {
          for (m = i; m < k; m++) keep[m] = 0
        }
        i = k
      }
      prev_blank = 1
      for (i = 1; i <= N; i++) {
        if (!keep[i]) continue
        if (T[i] == "B") {
          if (prev_blank) continue
          print ""
          prev_blank = 1
        } else {
          print L[i]
          prev_blank = 0
        }
      }
    }
  '
}

function write_header() {
  cat <<'EOF'
# Consumer Makefile — delegates canonical targets to .standards/templates/Makefile.canonical.
# Originally cloned from the pre-include-era templates/Makefile; migrated
# by migrate-makefile.sh. The original is preserved at Makefile.pre-migrate.bak.

ifeq (,$(wildcard .standards/templates/Makefile.canonical))
$(error .standards/templates/Makefile.canonical not found. Run: git submodule update --init --recursive)
endif

include .standards/templates/Makefile.canonical

## Consumer-specific content (preserved from the original Makefile).
## Targets whose recipes matched canonical were removed by the migration.
## Targets that drifted from canonical (see migration report) remain below
## and should be reconciled by hand.

EOF
}

# Emit the original Makefile content with removable target blocks stripped.
# A target block spans the target header line through the next blank line
# or next non-tab/non-target line. Also rewrites .PHONY lines (including
# backslash-continued multi-line forms) to remove canonical target names,
# and collapses runs of blank lines to a single blank.
function write_remainder() {
  local -r removable="${1}"
  local removable_file
  removable_file="$(mktemp -t migrate-removable.XXXXXX)"
  printf '%s\n' "${removable}" > "${removable_file}"
  awk -v removable_file="${removable_file}" '
    BEGIN {
      while ((getline line < removable_file) > 0) {
        if (line != "") removable[line] = 1
      }
      close(removable_file)
      in_skip = 0
      last_blank = 1
      bn = 0
      in_header = 1
    }
    function emit(s) {
      if (s == "") {
        if (last_blank) return
        print ""
        last_blank = 1
        return
      }
      print s
      last_blank = 0
    }
    function flush_buf() {
      for (i = 1; i <= bn; i++) emit(buf[i])
      bn = 0
    }
    function process_real(emit_line, line) {
      if (in_header) {
        bn = 0
        in_header = 0
      } else if (emit_line) {
        flush_buf()
      } else {
        bn = 0
      }
      if (emit_line) emit(line)
    }
    /^[[:space:]]*#/ { if (!in_skip) buf[++bn] = $0; next }
    /^$/ { in_skip = 0; buf[++bn] = ""; next }
    /^[[:space:]]*\.PHONY[[:space:]]*:/ {
      full = $0
      while (sub(/\\[[:space:]]*$/, "", full)) {
        if ((getline cont) <= 0) break
        full = full " " cont
      }
      sub(/^[^:]*:[[:space:]]*/, "", full)
      m = split(full, names, /[[:space:]]+/)
      out = ""
      for (i = 1; i <= m; i++) {
        if (names[i] == "" || (names[i] in removable)) continue
        out = (out == "" ? names[i] : out " " names[i])
      }
      if (out != "") process_real(1, ".PHONY: " out)
      else            process_real(0, "")
      next
    }
    /^[a-zA-Z_][a-zA-Z0-9_.-]*[[:space:]]*:/ &&
    !/^[a-zA-Z_][a-zA-Z0-9_.-]*[[:space:]]*[?:+]?=/ {
      target = $0
      sub(/[[:space:]]*:.*$/, "", target)
      if (target in removable) { in_skip = 1; process_real(0, ""); next }
      in_skip = 0
      process_real(1, $0)
      next
    }
    /^\t/ { if (!in_skip) emit($0); next }
    { in_skip = 0; process_real(1, $0) }
  ' "${BACKUP_PATH}"
  rm -f "${removable_file}"
}

main "${@:-}"
