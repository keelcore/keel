#!/usr/bin/env bash
# scripts/lib/markdownlint.sh
# Where the pinned markdownlint lives. scripts/ci/setup-markdownlint.sh installs
# it with `npm ci` from the committed lockfile (scripts/ci/markdownlint/ in
# keelcore/standards; consumers read it from their .standards submodule) into a
# cache directory keyed by the lockfile's hash, so every package in the tree is
# the locked version with its locked integrity hash.
# Environment: XDG_CACHE_HOME (default ${HOME}/.cache).

# markdownlint_lock_dir: the directory holding package.json and package-lock.json.
declare -rx XDG_CACHE_HOME="${XDG_CACHE_HOME:-${HOME}/.cache}"

function markdownlint_lock_dir() {
  local -r here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  if [ -f "${here}/../ci/markdownlint/package-lock.json" ]; then
    printf '%s\n' "$(cd "${here}/../ci/markdownlint" && pwd)"
    return 0
  fi
  printf '%s/.standards/scripts/ci/markdownlint\n' "$(git rev-parse --show-toplevel)"
}

# markdownlint_install_dir: the npm ci prefix for the current lockfile.
function markdownlint_install_dir() {
  local lock_hash
  lock_hash="$(git hash-object "$(markdownlint_lock_dir)/package-lock.json")"
  printf '%s/keelcore/markdownlint/%s\n' "${XDG_CACHE_HOME}" "${lock_hash}"
}

# markdownlint_bin: the pinned markdownlint executable.
function markdownlint_bin() {
  printf '%s/node_modules/.bin/markdownlint\n' "$(markdownlint_install_dir)"
}
