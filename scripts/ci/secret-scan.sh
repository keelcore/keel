#!/usr/bin/env bash
# secret-scan.sh
# Scan repository for committed secrets using gitleaks.
# Runnable locally and in CI identically.
#
# On Linux CI runners, installs gitleaks automatically if not found.
# Locally, install gitleaks from https://github.com/gitleaks/gitleaks/releases

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

# Pin to a specific gitleaks version for reproducibility.
# Update this value when bumping gitleaks; verify the new release SHA before merging.
readonly GITLEAKS_VERSION='8.27.2'
readonly GITLEAKS_LINUX_SHA256='141c3b2dede46d8b3a53b47116da756bd223decc0374797559a6b50ecba5590c'
readonly GITLEAKS_ARCH='x64'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  log "Secret scan (gitleaks ${GITLEAKS_VERSION})"
  ensure_gitleaks
  run_scan
  log "Secret scan passed — no secrets detected"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_secret_scan.log' >&5
}

function validate_args() { :; }

function ensure_gitleaks() {
  if command -v gitleaks >/dev/null 2>&1; then
    return 0
  fi
  if [[ "$(uname -s)" != 'Linux' ]]; then
    log "ERROR: gitleaks not found. Install from https://github.com/gitleaks/gitleaks/releases"
    exit 1
  fi
  log "gitleaks not found; installing on Linux CI runner"
  install_gitleaks_linux
}

function install_gitleaks_linux() {
  local tmp
  tmp="$(mktemp -d)"
  download_gitleaks "${tmp}"
  verify_gitleaks "${tmp}"
  extract_and_install_gitleaks "${tmp}"
  rm -rf "${tmp}"
}

function download_gitleaks() {
  local -r tmp="${1}"
  local -r tarball="gitleaks_${GITLEAKS_VERSION}_linux_${GITLEAKS_ARCH}.tar.gz"
  local -r url="https://github.com/gitleaks/gitleaks/releases/download/v${GITLEAKS_VERSION}/${tarball}"
  curl -fsSL "${url}" -o "${tmp}/${tarball}"
}

function verify_gitleaks() {
  local -r tmp="${1}"
  local -r tarball="gitleaks_${GITLEAKS_VERSION}_linux_${GITLEAKS_ARCH}.tar.gz"
  printf '%s  %s/%s\n' "${GITLEAKS_LINUX_SHA256}" "${tmp}" "${tarball}" \
    | sha256sum --check --quiet
}

function extract_and_install_gitleaks() {
  local -r tmp="${1}"
  local -r tarball="gitleaks_${GITLEAKS_VERSION}_linux_${GITLEAKS_ARCH}.tar.gz"
  tar -xz -C "${tmp}" -f "${tmp}/${tarball}"
  sudo mv "${tmp}/gitleaks" /usr/local/bin/gitleaks
}

function run_scan() {
  # --exit-code 1 causes gitleaks to return 1 when leaks are found.
  # --no-banner suppresses decorative output; CI logs stay clean.
  # --config .gitleaks.toml applies project allowlists (false-positive suppression).
  gitleaks detect \
    --source . \
    --no-banner \
    --exit-code 1 \
    --config '.gitleaks.toml'
}

main "${@:-}"
