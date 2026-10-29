#!/usr/bin/env bash
# paths.sh
# Shared path filter helpers. Source this file; do not execute directly.

function submodule_paths() {
  git submodule foreach --quiet 'echo "$displaypath"' 2>/dev/null || true
}

# nolint_regex: emit a single extended-regex alternation built from the repo's
# .nolint file — one regex fragment per line, matched against tracked paths;
# blank lines and #-comments are ignored. Empty output ⇒ no exclusions. This
# lets a consumer exclude vendored/generated trees that live BELOW the repo root
# (e.g. a Go module's nested `vendor/`, which the top-level `^vendor/` filter and
# Go's own `./...` wildcard both miss) from every extension-based linter without
# editing canonical scripts.
function nolint_regex() {
  local root file
  root="$(git rev-parse --show-toplevel 2>/dev/null || echo '.')"
  file="${root}/.nolint"
  [ -f "${file}" ] || return 0
  grep -vE '^[[:space:]]*(#|$)' "${file}" | paste -sd '|' -
}

# grep_exclude: grep wrapper for exclusion filters. Exit 1 means "every line was
# excluded" (including empty input), which is a valid empty result, not a
# failure; only exit >= 2 (a real grep error) propagates.
function grep_exclude() {
  grep "$@" || [ "$?" -eq 1 ]
}

# nolint_filter: stdin→stdout, drop newline-delimited paths matching .nolint.
function nolint_filter() {
  local re
  re="$(nolint_regex)"
  [ -z "${re}" ] && { cat; return 0; }
  grep_exclude -Ev "${re}"
}

# nolint_filter0: NUL-delimited variant, for `git ls-files -z` pipelines.
function nolint_filter0() {
  local re
  re="$(nolint_regex)"
  [ -z "${re}" ] && { cat; return 0; }
  grep_exclude -zEv "${re}"
}

# filter_paths: stdin→stdout generic exclusion. %s is replaced with the path component.
# arg1: vendor pattern template  arg2: submodule pattern template
function filter_paths() {
  local pattern="${1//%s/vendor}"
  while IFS= read -r sm; do
    [ -z "${sm}" ] && continue
    pattern="${pattern}|${2//%s/${sm}}"
  done < <(submodule_paths)
  grep_exclude -Ev "${pattern}"
}

# filter_src also honors .nolint so nested vendored trees are excluded from
# file-path linters (newlines, go_source_files). filter_pkgs stays package-path
# only (go list already drops vendor) and is intentionally left alone.
function filter_src()  { filter_paths '^%s/' '^%s/' | nolint_filter; }
function filter_pkgs() { filter_paths '/%s/' '/%s/|/%s$'; }

function go_source_files() { git ls-files '*.go' | filter_src; }
function go_pkgs()         { go list ./... | filter_pkgs; }

# source_files: the repo-owned source files matching the given pathspec globs
# (e.g. `source_files '*.sh' '*.bash'`), with vendored / generated / submodule
# trees excluded by construction (filter_src honors top-level `^vendor/`, nested
# `vendor/` at any depth via .nolint, and submodule paths). This is the single
# generator every extension-based linter should iterate: never a raw
# `git ls-files` (which includes vendored trees) and never a per-file grep loop.
function source_files() { git ls-files -- "$@" | filter_src; }
