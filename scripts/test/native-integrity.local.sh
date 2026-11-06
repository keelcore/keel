#!/usr/bin/env bash
# scripts/test/native-integrity.local.sh
# keel's native run: the BATS integrity suite against the keel binaries in dist/.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# Artifacts lose their executable bits in transit.
chmod +x dist/keel-*
bash scripts/test/bats-integrity.sh
