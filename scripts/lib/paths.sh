#!/usr/bin/env bash
# paths.sh
# Shared path filter helpers. Source this file; do not execute directly.

function submodule_paths() {
  git submodule foreach --quiet 'echo "$displaypath"' 2>/dev/null || true
}

# filter_paths: stdin→stdout generic exclusion. %s is replaced with the path component.
# arg1: vendor pattern template  arg2: submodule pattern template
function filter_paths() {
  local pattern="${1//%s/vendor}"
  while IFS= read -r sm; do
    [ -z "${sm}" ] && continue
    pattern="${pattern}|${2//%s/${sm}}"
  done < <(submodule_paths)
  grep -Ev "${pattern}"
}

function filter_src()  { filter_paths '^%s/' '^%s/'; }
function filter_pkgs() { filter_paths '/%s/' '/%s/|/%s$'; }

function go_source_files() { git ls-files '*.go' | filter_src; }
function go_pkgs()         { go list ./... | filter_pkgs; }
