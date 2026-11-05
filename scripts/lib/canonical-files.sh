#!/usr/bin/env bash
# canonical-files.sh
# Canonical files outside scripts/. Source this file; do not execute directly.

# canonical_workflows: the workflows every repository carries byte-identical. They are copies,
# not reusable workflows: Scorecard refuses to publish from a reusable workflow.
function canonical_workflows() {
  printf '%s\n' '.github/workflows/scorecard.yml' '.github/workflows/codeql.yml'
}
