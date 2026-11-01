#!/usr/bin/env bash
# scripts/lint/helm-policy.sh
# Chart policy scan: renders helm/keel in library and sidecar mode and runs
# kubescape on each render. Fails on any failed control not in ALLOWED_CONTROLS.
# Install with: make setup-helm setup-kubescape
#
# Allowed controls (each is a property of the install, not of the chart):
#   C-0012 credentials in config — flags keys named *key_file in keel.yaml; they hold file paths,
#          and secrets live in the keel-secrets Secret
#   C-0061 default namespace     — the namespace is chosen at helm install time
#   C-0076 label usage           — the chart uses app.kubernetes.io/* labels, not "app"
#   C-0211 security context      — seLinuxOptions and fsGroup are cluster-specific; PSS-restricted
#          settings (non-root, seccomp, no privilege escalation, drop ALL) are set
#   C-0236 / C-0237 image signature — verified at admission (cosign policy), not in a render
#   C-0258 ConfigMap access      — keel reads its keel.yaml from its own ConfigMap by design
# Sidecar mode also allows C-0018 / C-0056 (probes): the chart has no probe values for the
# user's app container; keel's own container has all three probes.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly CHART_DIR='helm/keel'
readonly SIDECAR_VALUES='tests/fixtures/helm-policy/sidecar-values.yaml'
readonly ALLOWED_CONTROLS='C-0012 C-0061 C-0076 C-0211 C-0236 C-0237 C-0258'
readonly SIDECAR_ALLOWED='C-0018 C-0056'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  require_tool helm
  require_tool kubescape
  local work
  work="$(mktemp -d)"
  helm template keel "${CHART_DIR}" --set mode=library > "${work}/library.yaml"
  helm template keel "${CHART_DIR}" -f "${SIDECAR_VALUES}" > "${work}/sidecar.yaml"
  local failed=0
  check_render "${work}" library "${ALLOWED_CONTROLS}" || failed=1
  check_render "${work}" sidecar "${ALLOWED_CONTROLS} ${SIDECAR_ALLOWED}" || failed=1
  rm -rf "${work}"
  [ "${failed}" -eq 0 ] || exit 1
  log '✅ Chart policy scan clean'
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_lint_helm_policy.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

function check_render() {
  local -r work="${1}"
  local -r mode="${2}"
  local -r allowed="${3}"
  log "🔍 kubescape: ${mode} mode"
  run_kubescape "${work}" "${mode}"
  local unexpected
  unexpected="$(failed_controls "${work}/${mode}.json" | grep -vxF -f <(tr ' ' '\n' <<< "${allowed}") || true)"
  [ -z "${unexpected}" ] && return 0
  log "❌ ${mode} mode fails controls:"
  log "${unexpected}"
  return 1
}

function run_kubescape() {
  local -r work="${1}"
  local -r mode="${2}"
  kubescape scan "${work}/${mode}.yaml" --format json --output "${work}/${mode}.json" >/dev/null
}

function require_tool() {
  local -r tool="${1}"
  if ! command -v "${tool}" >/dev/null 2>&1; then
    log "ERROR: ${tool} not found in PATH"
    log "  Install via: make setup-${tool}"
    exit 1
  fi
}

# failed_controls prints the ID of each failed control.
function failed_controls() {
  jq -r '.summaryDetails.controls | to_entries[] | select(.value.status == "failed") | .key' "${1}"
}

main "${@:-}"
