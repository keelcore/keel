#!/usr/bin/env bash
# scripts/lib/release-tag.sh
# The tag a canonical release script publishes, waits for or checks. A manual (workflow_dispatch) run names its
# tag in INPUT_TAG; on a tag push GitHub sets GITHUB_REF_NAME to the tag. On any other run GITHUB_REF_NAME is a
# branch, and GitHub ignores a workflow's assignment to it, so it is the tag only when GITHUB_REF_TYPE is tag.
# Environment: INPUT_TAG (default empty), GITHUB_REF_NAME (default empty), GITHUB_REF_TYPE (default empty).
# Source this file.

declare -rx INPUT_TAG="${INPUT_TAG:-}"
declare -rx GITHUB_REF_NAME="${GITHUB_REF_NAME:-}"
declare -rx GITHUB_REF_TYPE="${GITHUB_REF_TYPE:-}"

# release_tag: INPUT_TAG when set, else GITHUB_REF_NAME on a tag push; never a branch name. Otherwise it fails,
# naming both, on stderr.
function release_tag() {
  if [ -n "${INPUT_TAG}" ]; then
    printf '%s' "${INPUT_TAG}"
  elif [ "${GITHUB_REF_TYPE}" = 'tag' ] && [ -n "${GITHUB_REF_NAME}" ]; then
    printf '%s' "${GITHUB_REF_NAME}"
  else
    printf '❌ No release tag: INPUT_TAG is unset, and GITHUB_REF_NAME is not a tag (GITHUB_REF_TYPE=%s)\n' \
      "${GITHUB_REF_TYPE:-unset}" >&2
    return 1
  fi
}
