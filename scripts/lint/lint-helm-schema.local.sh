#!/usr/bin/env bash
# scripts/lint/lint-helm-schema.local.sh
# keel's Helm schema audit (make lint-helm-schema): the chart's values follow the config schema
# (pkg/config/schema.yaml), the one hierarchy keel.yaml and the env names follow (#41). For each values
# scenario it renders the chart's keel.yaml and checks that each section appears once and that keel loads it
# (keel --validate: the schema refuses a key it does not define and a value of the wrong type); that a keel.config
# override replaces the rendered value; and that VALUES, which set every schema field but the secrets (those come
# from keel-secrets.yaml or the environment), render every one of them. Project-owned, never canonical.
# Usage: lint-helm-schema.local.sh [CHART [VALUES]]
#   (defaults: helm/keel, tests/fixtures/helm/schema-values.yaml)

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly REPO_ROOT

# Schema fields a ConfigMap never carries: keel-secrets.yaml or the environment set them.
readonly SECRET_FIELDS='admin.metrics_token admin.token authn.oidc.client_secret'

WORK_DIR=''

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local -r chart="${1:-${REPO_ROOT}/helm/keel}"
  local -r values="${2:-${REPO_ROOT}/tests/fixtures/helm/schema-values.yaml}"
  require_tool helm
  require_tool go
  WORK_DIR="$(mktemp -d)"
  trap 'rm -rf "${WORK_DIR}"' EXIT
  log '🔨 Building keel to load the rendered config...'
  (cd "${REPO_ROOT}" && go build -o "${WORK_DIR}/keel" ./cmd/keel)
  local failed=0
  check_loads "${chart}" sidecar --set mode=sidecar --set sidecar.app.image=placeholder || failed=1
  check_loads "${chart}" library --set mode=library || failed=1
  check_loads "${chart}" container --values "${REPO_ROOT}/tests/fixtures/container/values.yaml" || failed=1
  check_loads "${chart}" full --values "${values}" || failed=1
  check_override "${chart}" || failed=1
  check_coverage || failed=1
  if [ "${failed}" -ne 0 ]; then
    log '❌ helm/keel values drift from the config schema'
    exit 1
  fi
  log '✅ helm/keel values follow the config schema'
}

# render_config CHART NAME HELM_ARGS...: the chart's keel.yaml for these values, in WORK_DIR/NAME.yaml.
function render_config() {
  local -r chart="${1}" name="${2}"
  shift 2
  helm template keel-schema "${chart}" --show-only templates/configmap.yaml "$@" \
    | awk '/^  keel\.yaml: \|/ { found = 1; next } found && /^  [^ ]/ { exit } found { sub(/^    /, ""); print }' \
      > "${WORK_DIR}/${name}.yaml"
}

# check_loads CHART NAME HELM_ARGS...: the rendered keel.yaml names each section once, and keel loads it.
function check_loads() {
  local -r chart="${1}" name="${2}"
  shift 2
  render_config "${chart}" "${name}" "$@"
  local -r file="${WORK_DIR}/${name}.yaml"
  local dupes
  dupes="$(awk '/^  [a-z_]+:/ { print $1 }' "${file}" | sort | uniq -d)"
  if [ -n "${dupes}" ]; then
    log "❌ ${name}: keel.yaml repeats sections: $(tr '\n' ' ' <<< "${dupes}")"
    return 1
  fi
  if ! env -i HOME="${WORK_DIR}" KEEL_KNOBS_GLOBAL_PATH="${file}" "${WORK_DIR}/keel" --validate \
    > "${WORK_DIR}/${name}.log" 2>&1; then
    log "❌ ${name}: keel refuses the rendered keel.yaml:"
    cat "${WORK_DIR}/${name}.log" >&5
    return 1
  fi
  log "✅ ${name}: keel loads the rendered keel.yaml"
}

# check_override CHART: keel.config replaces a rendered value instead of repeating its section.
function check_override() {
  local -r chart="${1}"
  check_loads "${chart}" override --set mode=library --set keel.config.listeners.http.port=8081 || return 1
  if ! leaf_paths "${WORK_DIR}/override.yaml" | grep -qxF 'listeners.http.port=8081'; then
    log '❌ override: keel.config.listeners.http.port=8081 did not set listeners.http.port'
    return 1
  fi
  log '✅ override: keel.config replaces the rendered value'
}

# check_coverage: the full values render every schema field but the secrets, and no other.
function check_coverage() {
  local -r fields="${WORK_DIR}/fields.txt" rendered="${WORK_DIR}/rendered.txt"
  (cd "${REPO_ROOT}" && go run ./cmd/config-schema/ --fields < pkg/config/schema.yaml) \
    | grep -vxF -f <(tr ' ' '\n' <<< "${SECRET_FIELDS}") | sort -u > "${fields}"
  leaf_paths "${WORK_DIR}/full.yaml" | sed 's/=.*//' | sort -u > "${rendered}"
  local missing extra
  missing="$(comm -23 "${fields}" "${rendered}")"
  extra="$(comm -13 "${fields}" "${rendered}")"
  [ -z "${missing}" ] || log "❌ schema fields the chart does not render: $(tr '\n' ' ' <<< "${missing}")"
  [ -z "${extra}" ] || log "❌ chart keys outside the schema: $(tr '\n' ' ' <<< "${extra}")"
  [ -z "${missing}${extra}" ] || return 1
  log '✅ full: every schema field but the secrets renders'
}

# leaf_paths FILE: each leaf of a rendered keel.yaml as path=value, the path below the keel: root. A key
# whose value is a block list is a leaf; its items are not listed.
function leaf_paths() {
  awk '
    function flush(next_ind, next_line) {
      if (!pending) return
      if (next_line ~ /^- / || next_ind <= pending_ind) print pending_path "="
      pending = 0
    }
    {
      match($0, /^ */); ind = RLENGTH; line = substr($0, ind + 1)
      if (line == "" || line ~ /^#/) next
      flush(ind, line)
      if (line ~ /^- /) next
      key = line; sub(/:.*/, "", key)
      val = line; sub(/^[^:]*:[ ]?/, "", val)
      depth = ind / 2; path[depth] = key
      p = ""; for (i = 1; i <= depth; i++) p = p (i > 1 ? "." : "") path[i]
      if (depth == 0) next
      if (val != "") print p "=" val
      else { pending = 1; pending_ind = ind; pending_path = p }
    }
    END { flush(-1, "") }
  ' "${1}"
}

function require_tool() {
  command -v "${1}" > /dev/null 2>&1 && return 0
  log "❌ ${1} not found; the tool image carries it"
  exit 1
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/lint_helm_schema.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 2 ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
