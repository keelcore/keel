#!/usr/bin/env bash
# bootstrap-standards.sh
# First-time setup for a consumer repo: add `.standards` as a submodule,
# wire AI adapters, copy starter templates (Makefile, BATS scaffolding,
# .gitleaks.toml, .markdownlintignore, .prettierrc, .local-claude.md) and
# the markdownlint config, then delegate the full canonical-script sync to
# `governance-refresh.sh`. No file content is embedded inline.
#
# Architecture:
#   - Canonical scripts (scripts/**) come from one place: governance-refresh.sh.
#     Bootstrap copies governance-refresh.sh from .standards as a seed and
#     invokes it; the script then walks .standards/scripts/ and copies any
#     missing or content-divergent file into the consumer. `.standards`
#     always wins for canonical scripts — operator hand-edits are overwritten
#     on every refresh.
#   - Templates (Makefile, BATS, configs, .local-claude.md) are create-only:
#     if the target already exists the operator's customizations are
#     preserved. The Makefile is seeded from .standards/templates/Makefile.consumer
#     (which `include`s .standards/templates/Makefile.canonical) so the
#     consumer starts with the canonical universal targets wired in.
#
# Non-greenfield handling:
#   - If CLAUDE.md exists and is NOT the canonical symlink, move it to
#     .local-claude.md so its content survives, then symlink the adapter.
#     If .local-claude.md already exists, warn and leave CLAUDE.md alone.
#   - If Makefile (or any other template target) exists, it is NOT
#     overwritten; governance-refresh handles Makefile target injection
#     for canonical targets the consumer is missing.
#   - Project-specific scripts living alongside canonical ones (e.g.
#     fetch-data.sh) are never touched.
#
# For ongoing reconciliation after bootstrap, the consumer runs
# `make governance-refresh`.
#
# Run locally:  bash .standards/scripts/bootstrap-standards.sh

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

function main() {
  exec 5>&1
  validate_args "${@:-}"
  init_submodule
  copy_markdownlint_config
  copy_templates
  seed_governance_refresh
  run_governance_refresh
  chmod_scripts
  install_tools
  create_adapters
  install_precommit_hook
  log '✅ Bootstrap complete'
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/bootstrap_standards.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 0 ] && [ -n "${1:-}" ]; then
    log '❌ Usage: bootstrap-standards.sh'
    exit 1
  fi
}

function init_submodule() {
  log '📦 Initializing .standards submodule...'
  git submodule add git@github.com:keelcore/standards .standards || true
  git submodule update --init --recursive
}

function install_tools() {
  log '🔧 Installing tools...'
  bash scripts/ci/setup-shellcheck.sh
  bash scripts/ci/setup-markdownlint.sh
  bash scripts/ci/setup-syft.sh
}

function create_claude_adapter() {
  local -r canonical_target='.standards/adapters/claude/CLAUDE.md'
  if [ -L CLAUDE.md ]; then
    log '  ✅ CLAUDE.md already a symlink; refreshing target'
    ln -sf "${canonical_target}" CLAUDE.md
    return 0
  fi
  if [ -f CLAUDE.md ]; then
    if [ -f .local-claude.md ]; then
      log '  ⚠️  CLAUDE.md and .local-claude.md both exist; leaving CLAUDE.md'
      log '     untouched. Reconcile manually before re-running bootstrap.'
      return 0
    fi
    log '  ↪ Existing CLAUDE.md found; moving to .local-claude.md'
    log '    (canonical adapter @-includes .local-claude.md, so content survives)'
    mv CLAUDE.md .local-claude.md
  fi
  ln -sf "${canonical_target}" CLAUDE.md
  log '  ✅ CLAUDE.md → .standards/adapters/claude/CLAUDE.md'
}

function create_copilot_adapter() {
  mkdir -p .github
  ln -sf ../.standards/adapters/copilot/copilot-instructions.md \
    .github/copilot-instructions.md
  log '  ✅ .github/copilot-instructions.md symlinked'
}

function create_cursor_rules() {
  mkdir -p .cursor/rules
  local f
  for f in .standards/adapters/cursor/*.mdc; do
    local dest
    dest=".cursor/rules/$(basename "${f}")"
    sed 's|@../../governance/|@../../.standards/governance/|g' "${f}" > "${dest}"
  done
  log '  ✅ .cursor/rules/*.mdc copied and path-adjusted'
}

function create_adapters() {
  log '🔗 Creating AI adapters...'
  create_claude_adapter
  create_copilot_adapter
  create_cursor_rules
}

function seed_governance_refresh() {
  log '📥 Seeding scripts/governance-refresh.sh from .standards...'
  mkdir -p scripts
  cp .standards/scripts/governance-refresh.sh scripts/governance-refresh.sh
  chmod +x scripts/governance-refresh.sh
}

function run_governance_refresh() {
  log '🔄 Syncing canonical scripts and Makefile targets via governance-refresh...'
  bash scripts/governance-refresh.sh
}

function copy_markdownlint_config() {
  log '📄 Copying markdownlint config...'
  cp .standards/.markdownlint.json .markdownlint.json
}

function _maybe_copy_template() {
  local -r src="${1}"
  local -r dst="${2}"
  if [ -e "${dst}" ]; then
    log "  ⏭️  ${dst} exists; preserving operator customizations"
    return 0
  fi
  mkdir -p "$(dirname "${dst}")"
  cp "${src}" "${dst}"
  log "  ✅ ${dst} created from template"
}

function copy_templates() {
  log '📄 Copying starter templates (create-if-missing)...'
  local -r src='.standards/templates'
  _maybe_copy_template "${src}/Makefile.consumer"           Makefile
  _maybe_copy_template "${src}/.gitleaks.toml"              .gitleaks.toml
  _maybe_copy_template "${src}/.markdownlintignore"         .markdownlintignore
  _maybe_copy_template "${src}/.prettierrc"                 .prettierrc
  _maybe_copy_template "${src}/.local-claude.md"            .local-claude.md
  _maybe_copy_template "${src}/tests/canonical/smoke.bats"  tests/canonical/smoke.bats
  _maybe_copy_template "${src}/tests/integrity.bats"        tests/integrity.bats
  _maybe_copy_template "${src}/tests/fixtures/.gitkeep"     tests/fixtures/.gitkeep
}

function chmod_scripts() {
  log '🔒 Setting script permissions...'
  find scripts -name '*.sh' -exec chmod +x '{}' ';'
}

function install_precommit_hook() {
  log '🪝 Installing git pre-commit hook...'
  bash scripts/install-hooks.sh
}

main "${@:-}"
