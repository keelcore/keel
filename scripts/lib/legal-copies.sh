#!/usr/bin/env bash
# scripts/lib/legal-copies.sh
# The legal files a repository keeps a copy of (a LICENSE beside code that embeds it), listed in
# config/legal-copies.local.txt: one pair per line, SOURCE<TAB>COPY, both relative to the repository root.
# Blank lines and lines starting with # are skipped. Each repository owns its list; refresh-governance never
# syncs it. Read by check-legal-drift.sh and ensure-legal-notices.sh. Source this file.

declare -r LEGAL_COPIES_FILE='config/legal-copies.local.txt'

# legal_copies: prints each listed pair as SOURCE<TAB>COPY, nothing when the list is absent. A malformed line
# (not two non-empty tab-separated paths, an absolute or .. path, a copy of itself) fails, naming FILE:LINE on
# stderr, before any pair is printed.
function legal_copies() {
  [ -f "${LEGAL_COPIES_FILE}" ] || return 0
  local line src dst pairs='' n=0 bad=0
  while IFS= read -r line || [ -n "${line}" ]; do
    n=$((n + 1))
    [[ "${line}" =~ ^[[:space:]]*(#.*)?$ ]] && continue
    src="${line%%$'\t'*}"
    dst="${line#*$'\t'}"
    if [[ "${line}" != *$'\t'* ]] || [ -z "${src}" ] || [ -z "${dst}" ] || [[ "${dst}" == *$'\t'* ]]; then
      legal_copies_bad "${n}" 'expected SOURCE<TAB>COPY'
      bad=1
      continue
    fi
    if ! legal_copies_relative "${src}" || ! legal_copies_relative "${dst}"; then
      legal_copies_bad "${n}" 'paths are relative to the repository root, with no ..'
      bad=1
      continue
    fi
    if [ "${src}" = "${dst}" ]; then
      legal_copies_bad "${n}" 'a file is not a copy of itself'
      bad=1
      continue
    fi
    pairs+="${src}"$'\t'"${dst}"$'\n'
  done < "${LEGAL_COPIES_FILE}"
  [ "${bad}" -eq 0 ] || return 1
  printf '%s' "${pairs}"
}

function legal_copies_bad() {
  printf '❌ %s:%s: %s\n' "${LEGAL_COPIES_FILE}" "${1}" "${2}" >&2
}

function legal_copies_relative() {
  [[ "${1}" != /* ]] && [[ "/${1}/" != */../* ]]
}
