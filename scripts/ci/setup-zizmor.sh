#!/usr/bin/env bash
# setup-zizmor.sh
# Install zizmor for GitHub Actions workflow security lint (scripts/lint/workflows.sh).
# Linux (CI runners): pinned v1.30.1 release asset, SHA256-verified before install.
# macOS: Homebrew.
# No-op if zizmor is already present.
#
# Pinned version: v1.30.1
# Update ZIZMOR_VERSION and ZIZMOR_SHA256 together when bumping.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly ZIZMOR_VERSION='1.30.1'
readonly ZIZMOR_SHA256='e65324f4430c2717591937edcec90ccbefaf14c174f8ec9415e03ca875b46e1a'
readonly ZIZMOR_ASSET="zizmor-x86_64-unknown-linux-gnu.tar.gz"
readonly ZIZMOR_URL="https://github.com/zizmorcore/zizmor/releases/download/v${ZIZMOR_VERSION}/${ZIZMOR_ASSET}"
readonly ZIZMOR_INSTALL_PATH='/usr/local/bin/zizmor'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  if command -v zizmor >/dev/null 2>&1; then
    log "zizmor already installed: $(zizmor --version 2>&1 | head -1)"
    return 0
  fi
  case "$(uname -s)" in
    Linux)
      log "Installing zizmor v${ZIZMOR_VERSION} (Linux)"
      install_linux
      ;;
    Darwin)
      log "Installing zizmor via Homebrew"
      install_macos
      ;;
    *)
      log "ERROR: setup-zizmor.sh does not support OS: $(uname -s)"
      exit 1
      ;;
  esac
  log "zizmor installed: $(zizmor --version 2>&1 | head -1)"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_setup_zizmor.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected arg'
    exit 1
  fi
}

function install_linux() {
  local tmp
  tmp="$(mktemp -d)"
  curl -fsSL "${ZIZMOR_URL}" -o "${tmp}/${ZIZMOR_ASSET}"
  printf '%s  %s/%s\n' "${ZIZMOR_SHA256}" "${tmp}" "${ZIZMOR_ASSET}" | sha256sum --check --quiet
  stage_binary "${tmp}"
  sudo install -m 0755 "${tmp}/zizmor.staged" "${ZIZMOR_INSTALL_PATH}"
  rm -rf "${tmp}"
}

# stage_binary extracts the tarball and stages the zizmor binary wherever it sits in it.
function stage_binary() {
  local -r tmp="${1}"
  tar -xzf "${tmp}/${ZIZMOR_ASSET}" -C "${tmp}"
  find "${tmp}" -type f -name zizmor -perm -u+x -exec mv {} "${tmp}/zizmor.staged" \;
  [ -f "${tmp}/zizmor.staged" ] || { log "ERROR: no zizmor binary in ${ZIZMOR_ASSET}"; exit 1; }
}

function install_macos() {
  brew install zizmor
}

main "${@:-}"
