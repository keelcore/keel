#!/usr/bin/env bash
# roadmap-issues.sh
# Syncs ROADMAP.md entries to GitHub issues. Idempotent: skips any entry whose
# title already matches an open or closed issue. Safe to run periodically after
# adding roadmap items.
#
# Usage:  scripts/roadmap-issues.sh
# Needs:  gh (GitHub CLI), authenticated with repo write access.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly LABEL='roadmap'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  require_gh
  ensure_label
  sync_roadmap
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_roadmap_issues.log' >&5
}

function validate_args() { :; }

function require_gh() {
  if ! command -v gh >/dev/null 2>&1; then
    log "ERROR: gh (GitHub CLI) is required. Install: https://cli.github.com"
    exit 1
  fi
  if ! gh auth status >/dev/null 2>&1; then
    log "ERROR: gh is not authenticated. Run: gh auth login"
    exit 1
  fi
}

function ensure_label() {
  if gh label list --limit 200 --json name | grep -qF "\"${LABEL}\""; then
    return 0
  fi
  log "Creating label '${LABEL}'"
  gh label create "${LABEL}" \
    --color '0075ca' \
    --description 'Planned roadmap item' >/dev/null
}

function sync_roadmap() {
  local repo_root roadmap repo_url
  repo_root="$(git rev-parse --show-toplevel)"
  roadmap="${repo_root}/docs/ROADMAP.md"
  if [[ ! -f "${roadmap}" ]]; then
    log "ERROR: not found: ${roadmap}"
    exit 1
  fi

  repo_url="$(gh repo view --json url -q '.url')/blob/main/docs/ROADMAP.md"

  log "Loading existing issues (open and closed)..."
  local existing_titles
  existing_titles="$(gh issue list --state all --limit 500 --json title --jq '.[].title')"

  log "Parsing ${roadmap}"
  log ""

  local title='' body='' in_entry=0 created=0 skipped=0

  while IFS= read -r line || [[ -n "${line}" ]]; do
    if [[ "${line}" =~ ^###[[:space:]](.+)$ ]]; then
      # Flush the previous entry before starting a new one.
      if [[ -n "${title}" ]]; then
        if _process_entry "${title}" "${body}" "${existing_titles}" "${repo_url}"; then
          created=$(( created + 1 ))
        else
          skipped=$(( skipped + 1 ))
        fi
      fi
      title="${BASH_REMATCH[1]}"
      body=''
      in_entry=1
    elif [[ "${line}" == '---' && "${in_entry}" -eq 1 && -n "${title}" ]]; then
      if _process_entry "${title}" "${body}" "${existing_titles}" "${repo_url}"; then
        created=$(( created + 1 ))
      else
        skipped=$(( skipped + 1 ))
      fi
      title=''; body=''; in_entry=0
    elif [[ "${in_entry}" -eq 1 && -n "${title}" ]]; then
      body+="${line}"$'\n'
    fi
  done < "${roadmap}"

  # Flush the final entry (file ends without a trailing ---).
  if [[ -n "${title}" ]]; then
    if _process_entry "${title}" "${body}" "${existing_titles}" "${repo_url}"; then
      created=$(( created + 1 ))
    else
      skipped=$(( skipped + 1 ))
    fi
  fi

  log ""
  log "Done.  Created: ${created}  Already on file: ${skipped}"
}

# _process_entry creates a GitHub issue for title+body unless an issue with
# that exact title already exists (open or closed).
# Returns 0 if created, 1 if already on file.
function _process_entry() {
  local -r title="${1}" body="${2}" existing_titles="${3}" repo_url="${4}"

  if printf '%s\n' "${existing_titles}" | grep -qxF "${title}"; then
    log "  SKIP  ${title}"
    return 1
  fi

  local footer url
  footer=$'\n\n_Source: [docs/ROADMAP.md]('"${repo_url}"')_'
  url="$(gh issue create \
    --title "${title}" \
    --body "${body}${footer}" \
    --label "${LABEL}")"
  log "  NEW   ${title}"
  log "        ${url}"
  return 0
}

main "${@:-}"
