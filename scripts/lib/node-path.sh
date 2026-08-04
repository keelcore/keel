#!/usr/bin/env bash
# node-path.sh
# Shared helper for ensuring `node` is on PATH. Source this file; do not
# execute directly.
#
# Several scripts invoke Node-based tools through shebangs of the form
# `#!/usr/bin/env node` (markdownlint-cli2, prettier, npx, etc.). Those
# binaries exit 127 with `env: node: No such file or directory` when node
# is absent from PATH — even when the tool binary itself is reachable via
# a project-local node_modules/.bin entry.
#
# The mismatch is common on macOS: developers install node via Homebrew,
# nvm, fnm, volta, mise, or asdf, each of which adds node to PATH via the
# interactive shell's init file (~/.zshrc / ~/.bashrc). Running scripts
# directly with `bash scripts/X.sh` does NOT source those init files, so
# PATH ends up node-less.
#
# This helper probes the well-known install locations for each manager,
# prepends the first directory it finds to PATH, and returns 0. It returns
# 1 (without changing PATH) only when no node binary can be located. The
# helper does not log — callers decide what to say to the user.

# shellcheck source=paths-ensure.sh
. "$(dirname "${BASH_SOURCE[0]}")/paths-ensure.sh"

# node_path_ensure: idempotent; safe to call multiple times.
function node_path_ensure() {
  # First restore Homebrew / system-wide PATH additions (where most
  # macOS users install node). This is the cheapest, most common hit.
  paths_ensure_standard
  if command -v node > /dev/null 2>&1; then
    return 0
  fi

  # Volta and per-user $HOME/.local installs are not on the standard
  # PATH set; probe explicitly.
  local -a candidates=(
    "${HOME}/.volta/bin/node"
    "${HOME}/.local/bin/node"
  )

  # Per-version installers (nvm, fnm, mise, asdf) place node under a
  # version-named directory. For each known root, take the lexically-last
  # `bin/node` so we pick a stable "latest installed" rather than forcing
  # the caller to know the exact pinned version.
  local vm_root latest_node
  for vm_root in \
      "${HOME}/.nvm/versions/node" \
      "${HOME}/.fnm/node-versions" \
      "${HOME}/Library/Application Support/fnm/node-versions" \
      "${HOME}/.local/share/mise/installs/node" \
      "${HOME}/.asdf/installs/nodejs"; do
    [ -d "${vm_root}" ] || continue
    latest_node="$(find "${vm_root}" -mindepth 2 -maxdepth 5 -name node -type f -perm -u+x 2>/dev/null | sort | tail -1)"
    if [ -n "${latest_node}" ] && [ -x "${latest_node}" ]; then
      candidates+=("${latest_node}")
    fi
  done

  local cand
  for cand in "${candidates[@]}"; do
    if [ -x "${cand}" ]; then
      PATH="$(dirname "${cand}"):${PATH}"
      export PATH
      return 0
    fi
  done

  return 1
}
