#!/usr/bin/env bash
# paths-ensure.sh
# Prepend well-known binary install dirs to PATH if they exist and are not
# already on PATH. Source this file; do not execute directly.
#
# `bash scripts/X.sh` does NOT source the user's interactive shell init
# files (~/.bashrc, ~/.zshrc, etc.), so PATH augmentations performed there
# — Homebrew shellenv, version managers, custom $HOME/bin prepends — are
# ABSENT in the bash subshell. Tools the user has clearly installed and
# uses interactively (`brew install shellcheck`, `brew install node`, etc.)
# then look "missing" because their install dir isn't on PATH.
#
# This helper restores the standard system-wide install locations. Tool-
# specific version-manager discovery (nvm/fnm/volta/asdf/mise for node)
# lives in lib/node-path.sh, which calls into here first before probing
# its own additional candidates.

# paths_ensure_standard: idempotent; safe to call multiple times.
function paths_ensure_standard() {
  local d
  for d in \
      /opt/homebrew/bin \
      /opt/homebrew/sbin \
      /usr/local/bin \
      /usr/local/sbin \
      /home/linuxbrew/.linuxbrew/bin \
      /home/linuxbrew/.linuxbrew/sbin; do
    if [ -d "${d}" ] && [[ ":${PATH:-}:" != *":${d}:"* ]]; then
      PATH="${d}:${PATH:-}"
    fi
  done
  export PATH
}
