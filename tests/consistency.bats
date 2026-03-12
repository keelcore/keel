#!/usr/bin/env bats
# tests/consistency.bats
# Enforces that the Go config struct, Helm chart values, Helm configmap template,
# and config-reference.md are globally consistent with each other.
#
# Run locally: bats tests/consistency.bats
# Run in CI:   included in the lint job via scripts/lint/go.sh or a dedicated step.
#
# Failure modes caught:
#   1. A yaml tag added to config.go but not rendered in the Helm configmap.
#   2. A yaml tag added to config.go but missing from config-reference.md.
#   3. A $v.xxx template variable in configmap.yaml with no matching key in values.yaml.
#   4. keel.* top-level keys present in values.yaml but absent from colima/values.yaml.
#   5. The Helm chart fails to render entirely (template errors).

setup_file() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/.." && pwd)"
  export REPO_ROOT

  export CONFIG_GO="${REPO_ROOT}/pkg/config/config.go"
  export VALUES="${REPO_ROOT}/helm/keel/values.yaml"
  export COLIMA_VALUES="${REPO_ROOT}/tests/fixtures/colima/values.yaml"
  export CONFIGMAP_TMPL="${REPO_ROOT}/helm/keel/templates/configmap.yaml"
  export CONFIG_REF="${REPO_ROOT}/docs/config-reference.md"
  export SCHEMA_FILE="${REPO_ROOT}/pkg/config/schema.yaml"

  # Render the chart once; expose the keel.yaml block from the ConfigMap.
  export RENDERED
  RENDERED="$(helm template consistency-test "${REPO_ROOT}/helm/keel" \
    --set 'mode=sidecar' \
    --set 'sidecar.app.image=placeholder' \
    --set 'keel.upstream.url=http://localhost:3000' 2>&1)"

  export RENDERED_CONFIG
  RENDERED_CONFIG="$(printf '%s\n' "${RENDERED}" | \
    awk '/  keel\.yaml: \|/{found=1; next} found && /^[^ ]/{exit} found{print}')"
}

# ---------------------------------------------------------------------------
# 1. Helm chart renders without error
# ---------------------------------------------------------------------------
@test "helm template renders without error" {
  echo "${RENDERED}" | grep -qv "^Error:" || {
    echo "helm template produced errors:"
    echo "${RENDERED}"
    false
  }
  [ -n "${RENDERED_CONFIG}" ] || {
    echo "Could not extract keel.yaml block from rendered ConfigMap"
    false
  }
}

# ---------------------------------------------------------------------------
# 2. Every yaml tag in schema.yaml appears in the rendered Helm configmap
# ---------------------------------------------------------------------------
@test "all schema.yaml leaf tags are rendered in the Helm configmap" {
  local tags failed=0
  tags="$(cd "${REPO_ROOT}" && go run ./cmd/config-schema/ --fields < "${SCHEMA_FILE}" | awk -F. '{print $NF}' | sort -u)"

  while IFS= read -r tag; do
    # Skip generic single-word tags that are structurally guaranteed (enabled, port, etc.)
    # and tags that are k8s-abstracted (cert_file/key_file are derived from secrets).
    case "${tag}" in
      enabled|port|insecure)                                      continue ;;
      cert_file|key_file)                                         continue ;;  # rendered conditionally from k8s secret
      ca_file|client_cert_file|client_key_file)                   continue ;;  # rendered conditionally from k8s secret
    esac
    if ! printf '%s\n' "${RENDERED_CONFIG}" | grep -qE "^\s+${tag}:"; then
      echo "MISSING from rendered configmap: '${tag}'"
      failed=1
    fi
  done <<< "${tags}"

  [ "${failed}" -eq 0 ]
}

# ---------------------------------------------------------------------------
# 3. Every yaml tag in schema.yaml appears in config-reference.md
# ---------------------------------------------------------------------------
@test "all schema.yaml leaf tags are documented in config-reference.md" {
  local tags failed=0
  tags="$(cd "${REPO_ROOT}" && go run ./cmd/config-schema/ --fields < "${SCHEMA_FILE}" | awk -F. '{print $NF}' | sort -u)"

  while IFS= read -r tag; do
    case "${tag}" in
      enabled|port|insecure) continue ;;
    esac
    if ! grep -qE "^\s*${tag}:" "${CONFIG_REF}"; then
      echo "MISSING from config-reference.md: '${tag}'"
      failed=1
    fi
  done <<< "${tags}"

  [ "${failed}" -eq 0 ]
}

# ---------------------------------------------------------------------------
# 4. Every $v.xxx template path in configmap.yaml has a key in values.yaml
# ---------------------------------------------------------------------------
@test "all configmap template variable paths resolve in values.yaml" {
  local failed=0

  # Extract dotted paths like extAuthz.failOpen from $v.extAuthz.failOpen references.
  while IFS= read -r path; do
    local key
    key="$(printf '%s' "${path}" | cut -d. -f1)"
    if ! grep -qE "^\s+${key}:" "${VALUES}"; then
      echo "Template uses \$v.${path} but top-level key '${key}' not in values.yaml"
      failed=1
    fi
  done < <(grep -oE '\$v\.[a-zA-Z][a-zA-Z0-9.]+' "${CONFIGMAP_TMPL}" | sed 's/\$v\.//' | sort -u)

  [ "${failed}" -eq 0 ]
}

# ---------------------------------------------------------------------------
# 5. colima/values.yaml has all keel.* top-level keys present in values.yaml
# ---------------------------------------------------------------------------
@test "colima/values.yaml contains all keel top-level keys from values.yaml" {
  local failed=0

  # Extract top-level keys under the keel: block in values.yaml.
  # Exclude Helm-specific extension fields not expected in fixture files.
  local keys
  keys="$(awk '/^keel:/{found=1; next} found && /^[^ ]/{exit} found && /^  [a-z]/{print $1}' \
    "${VALUES}" | tr -d ':' | grep -Ev '^(config|secrets|extraEnv|extraArgs|extraVolumeMounts|extraVolumes)$')"

  while IFS= read -r key; do
    if ! awk '/^keel:/{found=1; next} found && /^[^ ]/{exit} found' "${COLIMA_VALUES}" | \
        grep -qE "^\s+${key}:"; then
      echo "MISSING from colima/values.yaml under keel: '${key}'"
      failed=1
    fi
  done <<< "${keys}"

  [ "${failed}" -eq 0 ]
}

# ---------------------------------------------------------------------------
# 6. pkg/config/schema.yaml is up to date with config.go
# ---------------------------------------------------------------------------
@test "pkg/config/schema.yaml is up to date with config.go" {
  local generated
  generated="$(cd "${REPO_ROOT}" && go run ./cmd/config-schema/)"
  local committed
  committed="$(cat "${SCHEMA_FILE}")"
  [ "${generated}" = "${committed}" ]
}

# ---------------------------------------------------------------------------
# 7. gen-schema.sh writes valid JSON Schema YAML with the expected header
# ---------------------------------------------------------------------------
@test "gen-schema.sh produces valid YAML with json-schema.org 2019-09 header" {
  local tmpdir
  tmpdir="$(mktemp -d)"
  local out="${tmpdir}/schema.yaml"

  # Run gen-schema.sh, capture output to a temp file.
  (cd "${REPO_ROOT}" && bash scripts/release/gen-schema.sh)

  # Verify the committed file now contains the expected $schema keyword.
  grep -q 'https://json-schema.org/draft/2019-09/schema' "${SCHEMA_FILE}"

  # Verify it is parseable YAML (go run --fields exits 0 when input is valid).
  cd "${REPO_ROOT}" && go run ./cmd/config-schema/ --fields < "${SCHEMA_FILE}" > /dev/null

  rm -rf "${tmpdir}"
}