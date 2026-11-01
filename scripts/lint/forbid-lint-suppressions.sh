#!/usr/bin/env bash
# scripts/lint/forbid-lint-suppressions.sh
# Lint errors and warnings are never suppressed in source code, in any language:
# fix the finding at its source, or turn the rule off in the linter's git-versioned
# configuration (governance/coding.md, "Static Analysis Configuration").
#
# Fails if any repo-owned file contains a known suppression directive. The scan
# covers every repo-owned file whatever its type, so a new language is covered as
# soon as its linter's directive is in DIRECTIVES; adopting a linter whose
# directive is missing means adding it there. Markdown is the one type scanned
# narrowly (markdownlint's comment directive only), because prose quotes the
# other directives as examples.
#
# Allowed (NOT a disable — just a hint that tells the linter where to look):
#   # shellcheck source=<path>     (e.g., source=./lib/common.sh)
#
# File set: the vendoring-aware source_files generator (scripts/lib/paths.sh), so
# vendored, nested-vendored (.nolint) and submodule trees are never scanned; the
# content search is a SINGLE `git grep` per class.

set -o nounset
set -o errexit
set -o pipefail

declare REPO_ROOT
REPO_ROOT="$(git rev-parse --show-toplevel)"

declare SELF_REL
SELF_REL="$(realpath "${BASH_SOURCE[0]}")"
SELF_REL="${SELF_REL#"${REPO_ROOT}"/}"

# Suppression directives of known linters (extended regex). Add a linter's
# directive here when the linter is adopted.
readonly DIRECTIVES=(
  'shellcheck[[:space:]]+disable='                       # shellcheck
  '//[[:space:]]*nolint'                                 # golangci-lint
  '//[[:space:]]*lint:(file-)?ignore'                    # staticcheck
  '#[[:space:]]*nosec'                                   # gosec, bandit
  '(eslint|stylelint|tslint|jshint)[-:][[:space:]]*(disable|ignore)'
  '@ts-(ignore|nocheck|expect-error)'                    # TypeScript
  'biome-ignore'                                         # Biome
  '#[[:space:]]*noqa'                                    # flake8, ruff
  '(pylint|pyright|mypy):[[:space:]]*(disable|ignore)'
  '#[[:space:]]*type:[[:space:]]*ignore'                 # mypy
  'yamllint[[:space:]]+disable'                          # yamllint
  'hadolint[[:space:]]+ignore'                           # hadolint
  '(tflint-ignore|tfsec:ignore|trivy:ignore|checkov:skip|kics-scan[[:space:]]+ignore)'
  'nosemgrep'                                            # semgrep
  'NOSONAR'                                              # SonarQube
  'NOLINT'                                               # clang-tidy
  '#pragma[[:space:]]+(warning[[:space:]]+disable|(GCC|clang)[[:space:]]+diagnostic[[:space:]]+ignored)'
  '@Suppress(Warnings|Lint)?\('                          # Java, Kotlin, Android lint
  'SuppressMessage'                                      # .NET analyzers
  '#!?\[(allow|expect)\('                                # Rust (clippy, rustc)
  'rubocop:[[:space:]]*disable'                          # RuboCop
  'swiftlint:[[:space:]]*disable'                        # SwiftLint
  '(phpcs:(ignore|disable)|@phpstan-ignore|@psalm-suppress)'
  'luacheck:[[:space:]]*ignore'                          # luacheck
)

readonly MARKDOWN_DIRECTIVE='<!--[[:space:]]*markdownlint-(disable|capture)'

# shellcheck source=../lib/paths.sh
. "$(dirname "${BASH_SOURCE[0]}")/../lib/paths.sh"

function main() {
  exec 5>&1
  local rc=0 code_pattern
  code_pattern="$(IFS='|'; printf '%s' "${DIRECTIVES[*]}")"
  scan "${MARKDOWN_DIRECTIVE}" 'markdownlint directive' "$(markdown_files)" || rc=1
  scan "${code_pattern}" 'suppression directive' "$(code_files)" || rc=1
  if [ "${rc}" -eq 0 ]; then
    log '✅ no lint-suppression directives in tracked files'
  fi
  exit "${rc}"
}

function markdown_files() {
  source_files '*.md' '*.markdown' '*.mdc'
}

# code_files: every repo-owned file that is not markdown.
function code_files() {
  source_files | grep_exclude -vE '\.(md|markdown|mdc)$'
}

# scan PATTERN LABEL FILES: report each line matching PATTERN in FILES (one path
# per line), minus this script (it necessarily quotes the patterns). Binary
# files are skipped (git grep -I).
function scan() {
  local -r pat="${1}" label="${2}"
  local files matches
  files="$(printf '%s\n' "${3}" | grep_exclude -vxF "${SELF_REL}" | grep_exclude -v '^$')"
  [ -z "${files}" ] && return 0
  # NUL-delimit the file list into a SINGLE `git grep`.
  matches="$(printf '%s\n' "${files}" | tr '\n' '\0' \
             | xargs -0 git -C "${REPO_ROOT}" grep -InE "${pat}" -- 2>/dev/null || true)"
  [ -z "${matches}" ] && return 0
  log "❌ forbidden lint suppression (${label}):"
  printf '%s\n' "${matches}" | sed 's/^/   /' >&5
  log '   → fix the finding at its source, or configure the rule off in the linter config'
  return 1
}

function log() {
  printf '%s\n' "${1:-}" >&5
}

main "${@:-}"
