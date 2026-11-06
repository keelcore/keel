#!/usr/bin/env bash
# scripts/release/docker.local.sh
# Before keel's images build: the CA bundle image their Dockerfile copies an allowlist of roots from.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

bash scripts/build/certs-image.sh
