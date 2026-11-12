#!/usr/bin/env bats
# tests/consistency.bats
# Enforces that the Go config struct, Helm chart values, Helm configmap template,
# and config-reference.md are globally consistent with each other.
#
# Run locally: bats tests/consistency.bats
# Run in CI:   included in the lint job via scripts/lint/lint-go.sh or a dedicated step.
#
# Failure modes caught:
#   1. A yaml tag added to config.go but not rendered in the Helm configmap.
#   2. A yaml tag added to config.go but missing from config-reference.md.
#   3. A $v.xxx template variable in configmap.yaml with no matching key in values.yaml.
#   4. keel.* top-level keys present in values.yaml but absent from container/values.yaml.
#   5. The Helm chart fails to render entirely (template errors).
#   6. The Docker Compose and k8s integration stacks drift apart.

setup_file() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/.." && pwd)"
  export REPO_ROOT

  export CONFIG_GO="${REPO_ROOT}/pkg/config/config.go"
  export VALUES="${REPO_ROOT}/helm/keel/values.yaml"
  export CONTAINER_VALUES="${REPO_ROOT}/tests/fixtures/container/values.yaml"
  export CONFIGMAP_TMPL="${REPO_ROOT}/helm/keel/templates/configmap.yaml"
  export CONFIG_REF="${REPO_ROOT}/docs/config-reference.md"
  export SCHEMA_FILE="${REPO_ROOT}/pkg/config/schema.yaml"

  # Render the chart once; expose the keel.yaml block from the ConfigMap.
  export RENDERED
  # The chart's values list only what it needs; tests/fixtures/helm/schema-values.yaml sets every field.
  RENDERED="$(helm template consistency-test "${REPO_ROOT}/helm/keel" \
    --values "${REPO_ROOT}/tests/fixtures/helm/schema-values.yaml" \
    --set 'mode=sidecar' \
    --set 'sidecar.app.image=placeholder' \
    --set 'keel.sidecar.upstream_url=http://localhost:3000' 2>&1)"

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
      ca_file|client_cert_file|client_key_file|client_ca_file)    continue ;;  # rendered conditionally from k8s secret
      token|metrics_token|client_secret)                          continue ;;  # secrets: keel-secrets.yaml or env
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
# 5. container/values.yaml has all keel.* top-level keys present in values.yaml
# ---------------------------------------------------------------------------
@test "container/values.yaml contains all keel top-level keys from values.yaml" {
  local failed=0

  # Extract top-level keys under the keel: block in values.yaml.
  # Exclude Helm-specific extension fields not expected in fixture files.
  local keys
  keys="$(awk '/^keel:/{found=1; next} found && /^[^ ]/{exit} found && /^  [a-z]/{print $1}' \
    "${VALUES}" | tr -d ':' | grep -Ev '^(config|secrets|mounts|extraEnv|extraArgs|extraVolumeMounts|extraVolumes)$')"

  while IFS= read -r key; do
    if ! awk '/^keel:/{found=1; next} found && /^[^ ]/{exit} found' "${CONTAINER_VALUES}" | \
        grep -qE "^\s+${key}:"; then
      echo "MISSING from container/values.yaml under keel: '${key}'"
      failed=1
    fi
  done <<< "${keys}"

  [ "${failed}" -eq 0 ]
}

# ---------------------------------------------------------------------------
# Helm values follow the config schema (#41): values.keel takes every schema
# field under its schema name. make lint-helm-schema audits the chart against
# tests/fixtures/helm/schema-values.yaml, which sets every field; these tests
# show it passes, and fails on each way the values can drift from the schema.
# ---------------------------------------------------------------------------
@test "lint-helm-schema: the chart's keel.yaml loads in keel, covers the schema, and keel.config overrides in place (#41)" {
  run bash "${REPO_ROOT}/scripts/lint/lint-helm-schema.local.sh"
  echo "${output}"
  [ "${status}" -eq 0 ]
}

# schema_values EDIT...: a copy of the full-values fixture, edited by sed, in BATS_TEST_TMPDIR.
function schema_values() {
  local -r values="${BATS_TEST_TMPDIR}/schema-values.yaml"
  sed "$@" "${REPO_ROOT}/tests/fixtures/helm/schema-values.yaml" > "${values}"
  printf '%s' "${values}"
}

@test "lint-helm-schema: an unrecognized node fails (#41)" {
  local values
  values="$(schema_values -e 's/^  admin:$/  admin:\n    no_such_field: true/')"
  run bash "${REPO_ROOT}/scripts/lint/lint-helm-schema.local.sh" "${REPO_ROOT}/helm/keel" "${values}"
  echo "${output}"
  [ "${status}" -ne 0 ]
  [[ "${output}" == *admin.no_such_field* ]]
}

@test "lint-helm-schema: a schema node missing from the full values fails, naming it (#41)" {
  local values
  values="$(schema_values -e '/^    pprof: false$/d')"
  run bash "${REPO_ROOT}/scripts/lint/lint-helm-schema.local.sh" "${REPO_ROOT}/helm/keel" "${values}"
  echo "${output}"
  [ "${status}" -ne 0 ]
  [[ "${output}" == *admin.pprof* ]]
}

@test "lint-helm-schema: a node of the wrong type fails (#41)" {
  local values edit
  for edit in 's/^      port: 8080$/      port: not-a-port/' \
    's/^    pprof: false$/    pprof: "yes please"/' \
    's/^  admin:$/  admin: off/; /^    pprof: false$/d'; do
    values="$(schema_values -e "${edit}")"
    run bash "${REPO_ROOT}/scripts/lint/lint-helm-schema.local.sh" "${REPO_ROOT}/helm/keel" "${values}"
    echo "${edit}: ${output}"
    [ "${status}" -ne 0 ]
    [[ "${output}" == *invalid_type* ]]
  done
}

@test "helm values with nodes missing render only the nodes set, leaving the rest to keel's defaults (#41)" {
  local rendered
  rendered="$(helm template keel-missing "${REPO_ROOT}/helm/keel" --show-only templates/configmap.yaml \
    --set mode=library --set keel.logging.level=debug)"
  echo "${rendered}"
  grep -qE '^ +level: debug$' <<< "${rendered}"
  ! grep -qE '^ +(owasp_headers|pprof|trusted_ids):' <<< "${rendered}"
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
@test "generate-schema.sh produces valid YAML with json-schema.org 2019-09 header" {
  local tmpdir
  tmpdir="$(mktemp -d)"
  local out="${tmpdir}/schema.yaml"

  # Run generate-schema.sh, capture output to a temp file.
  (cd "${REPO_ROOT}" && bash scripts/release/generate-schema.sh)

  # Verify the committed file now contains the expected $schema keyword.
  grep -q 'https://json-schema.org/draft/2019-09/schema' "${SCHEMA_FILE}"

  # Verify it is parseable YAML (go run --fields exits 0 when input is valid).
  cd "${REPO_ROOT}" && go run ./cmd/config-schema/ --fields < "${SCHEMA_FILE}" > /dev/null

  rm -rf "${tmpdir}"
}
# ---------------------------------------------------------------------------
# 9. Both runtime Dockerfiles copy the identical trusted-root allowlist from
#    keel-certs (an image built from one file must not trust a different set).
# ---------------------------------------------------------------------------
@test "Dockerfile and Dockerfile.release trust the identical CA root allowlist" {
  local df="${REPO_ROOT}/scripts/deploy/docker"
  local a b
  a="$(grep -oE '/certs/[A-Za-z0-9_.-]+\.crt' "${df}/Dockerfile" | sort)"
  b="$(grep -oE '/certs/[A-Za-z0-9_.-]+\.crt' "${df}/Dockerfile.release" | sort)"
  [ -n "${a}" ]
  [ "${a}" = "${b}" ]
}

# ---------------------------------------------------------------------------
# 10–12. The two integration stacks stay in step: Docker Compose (Linux CI) and
#        the k8s stack (Apple container locally) define the same services, and
#        every port the Go compose suite uses is reachable in both.
# ---------------------------------------------------------------------------

# compose_ports: "<service> <port>/<proto>" for each published port.
compose_ports() {
  awk '
    /^services:/ { in_s = 1; next }
    in_s && /^[^ #]/ { in_s = 0 }
    in_s && /^  [a-z0-9-]+:[[:space:]]*$/ { svc = $1; sub(":", "", svc); in_p = 0; next }
    in_s && /^    [a-z_]+:/ { in_p = ($1 == "ports:"); next }
    in_s && in_p && /^      - / {
      p = $2; gsub(/"/, "", p); proto = "tcp"
      if (p ~ /\//) { split(p, a, "/"); p = a[1]; proto = a[2] }
      n = split(p, b, ":"); print svc, b[n] "/" tolower(proto)
    }' "${REPO_ROOT}/tests/compose/docker-compose.test.yaml" | sort -u
}

compose_services() {
  awk '
    /^services:/ { in_s = 1; next }
    in_s && /^[^ #]/ { in_s = 0 }
    in_s && /^  [a-z0-9-]+:[[:space:]]*$/ { svc = $1; sub(":", "", svc); print svc }
  ' "${REPO_ROOT}/tests/compose/docker-compose.test.yaml" | sort -u
}

# k8s_host_ports: "<deployment> <port>/<proto>" for each hostPort.
k8s_host_ports() {
  awk '
    /^kind:/ { dep = ($2 == "Deployment"); name = ""; next }
    dep && /^  name:/ && name == "" { name = $2; next }
    dep && /hostPort:/ {
      port = $0; sub(/.*hostPort:[[:space:]]*/, "", port); sub(/[^0-9].*/, "", port)
      proto = "tcp"; if ($0 ~ /protocol:[[:space:]]*UDP/) proto = "udp"
      print name, port "/" proto
    }' "${REPO_ROOT}/tests/compose/k8s-stack.yaml" | sort -u
}

# k8s_services: each Deployment, and each init container (a one-shot compose service).
k8s_services() {
  awk '
    /^kind:/ { dep = ($2 == "Deployment"); name = ""; init = 0; next }
    dep && /^  name:/ && name == "" { name = $2; print name }
    dep && /^      initContainers:/ { init = 1; next }
    init && /^      [a-z]/ { init = 0 }
    init && /^        - name:/ { print $3 }
  ' "${REPO_ROOT}/tests/compose/k8s-stack.yaml" | sort -u
}

@test "docker-compose.test.yaml and k8s-stack.yaml define the same services" {
  local c k
  c="$(compose_services)"
  k="$(k8s_services)"
  [ -n "${c}" ]
  diff <(printf '%s\n' "${c}") <(printf '%s\n' "${k}")
}

@test "every k8s-stack hostPort is published by the same docker-compose service" {
  local missing
  missing="$(comm -23 <(k8s_host_ports) <(compose_ports))"
  [ -z "${missing}" ] || { echo "hostPorts not published in docker-compose.test.yaml:"; echo "${missing}"; false; }
}

@test "every port the compose suite uses is a k8s-stack hostPort" {
  local name value host_ports missing=''
  host_ports="$(k8s_host_ports | awk '{ split($2, a, "/"); print a[1] }' | sort -u)"
  while read -r name value; do
    grep -qw "${name}" "${REPO_ROOT}"/tests/compose/*_test.go || continue
    grep -qx "${value}" <<< "${host_ports}" || missing+="${name}=${value} "
  done < <(awk '/^[[:space:]]+[A-Za-z0-9]+[[:space:]]*=[[:space:]]*[0-9]+/ { print $1, $3 }' \
    "${REPO_ROOT}/tests/compose/ports.go")
  [ -z "${missing}" ] || { echo "ports used by tests/compose but not hostPorts: ${missing}"; false; }
}

# ---------------------------------------------------------------------------
# Branch protection as code: scripts/admin/apply-rulesets.sh and .github/rulesets/*.json
# ---------------------------------------------------------------------------

# rulesets_fixture CONTEXT: a repository whose committed main ruleset requires CONTEXT, a
# workflow with a plain job and a matrix job, and a live copy (RULESETS_LIVE_DIR) equal to it.
rulesets_fixture() {
  FIX="${BATS_TEST_TMPDIR}/repo"
  mkdir -p "${FIX}/.github/rulesets" "${FIX}/.github/workflows" "${BATS_TEST_TMPDIR}/live"
  jq -n --arg c "${1}" '{name: "main protection", target: "branch", enforcement: "active",
    conditions: {ref_name: {exclude: [], include: ["~DEFAULT_BRANCH"]}},
    bypass_actors: [{actor_id: 5, actor_type: "RepositoryRole", bypass_mode: "always"}],
    rules: [{type: "deletion"}, {type: "required_status_checks", parameters: {required_status_checks: [{context: $c}]}}]}' \
    > "${FIX}/.github/rulesets/main.json"
  cp "${FIX}/.github/rulesets/main.json" "${BATS_TEST_TMPDIR}/live/main.json"
  printf 'jobs:\n  lint:\n    name: Lint\n    runs-on: x\n  unit:\n    name: Unit (${{ matrix.platform }})\n    runs-on: x\n  bare:\n    runs-on: x\n' \
    > "${FIX}/.github/workflows/ci.yml"
  export RULESETS_LIVE_DIR="${BATS_TEST_TMPDIR}/live"
  cd "${FIX}"
}

rulesets() {
  run bash "${REPO_ROOT}/scripts/admin/apply-rulesets.sh" "$@"
}

@test "rulesets check: live matching the committed JSON passes, whatever its key order or metadata" {
  rulesets_fixture Lint
  jq -S '. + {id: 1, node_id: "x", created_at: "t", updated_at: "t", source: "o/r", _links: {}} | .rules |= reverse' \
    .github/rulesets/main.json > "${RULESETS_LIVE_DIR}/main.json"
  rulesets check
  [ "${status}" -eq 0 ]
}

@test "rulesets check: a drifted live ruleset fails and names it" {
  rulesets_fixture Lint
  jq '.enforcement = "disabled"' .github/rulesets/main.json > "${RULESETS_LIVE_DIR}/main.json"
  rulesets check
  [ "${status}" -eq 1 ]
  [[ "${output}" == *'main protection'* ]] || false
  [[ "${output}" == *'disabled'* ]] || false
}

@test "rulesets check: a read-only view without bypass_actors compares the rest" {
  rulesets_fixture Lint
  jq 'del(.bypass_actors)' .github/rulesets/main.json > "${RULESETS_LIVE_DIR}/main.json"
  rulesets check
  [ "${status}" -eq 0 ]
  jq 'del(.bypass_actors) | .enforcement = "disabled"' .github/rulesets/main.json > "${RULESETS_LIVE_DIR}/main.json"
  rulesets check
  [ "${status}" -eq 1 ]
}

@test "rulesets contexts: a required check naming no workflow job fails and is named" {
  rulesets_fixture 'Unit (ubuntu-latest)x'
  jq '.rules[1].parameters.required_status_checks[0].context = "Gone"' .github/rulesets/main.json > m && mv m .github/rulesets/main.json
  rulesets contexts
  [ "${status}" -eq 1 ]
  [[ "${output}" == *'Gone'* ]] || false
}

@test "rulesets contexts: plain, matrix and unnamed jobs all count as existing" {
  local ctx
  for ctx in Lint 'Unit (linux)' bare; do
    rulesets_fixture "${ctx}"
    rulesets contexts
    [ "${status}" -eq 0 ] || { echo "${ctx}: ${output}"; return 1; }
  done
}

@test "rulesets apply: refuses without a token; PUTs the committed JSON to the ruleset's id" {
  rulesets_fixture Lint
  unset RULESETS_LIVE_DIR
  GH_TOKEN='' rulesets apply
  [ "${status}" -eq 1 ]
  [[ "${output}" == *'GH_TOKEN'* ]] || false
  mkdir -p "${BATS_TEST_TMPDIR}/bin"
  cat > "${BATS_TEST_TMPDIR}/bin/curl" <<STUB
#!/usr/bin/env bash
printf '%s\n' "\$*" >> "${BATS_TEST_TMPDIR}/curl.log"
case "\$*" in
  *'-X PUT'*) cat "\${@: -1}" > /dev/null; printf '{}' ;;
  *) printf '[{"id": 77, "name": "main protection"}]' ;;
esac
STUB
  chmod +x "${BATS_TEST_TMPDIR}/bin/curl"
  PATH="${BATS_TEST_TMPDIR}/bin:${PATH}" GH_TOKEN=t GITHUB_REPOSITORY=o/r rulesets apply
  [ "${status}" -eq 0 ]
  grep -q -- '-X PUT .*repos/o/r/rulesets/77' "${BATS_TEST_TMPDIR}/curl.log"
}

@test "committed rulesets: every required check names a job in this repository's workflows" {
  cd "${REPO_ROOT}"
  [ -f .github/rulesets/main.json ]
  [ -f .github/rulesets/release-tags.json ]
  rulesets contexts
  [ "${status}" -eq 0 ]
}

@test "docs/governance.md lists every required check of the main ruleset" {
  local ctx missing=''
  while IFS= read -r ctx; do
    grep -qF "\`${ctx}\`" "${REPO_ROOT}/docs/governance.md" || missing+="${ctx}; "
  done < <(jq -r '.rules[] | select(.type == "required_status_checks") | .parameters.required_status_checks[].context' \
    "${REPO_ROOT}/.github/rulesets/main.json")
  [ -z "${missing}" ] || { echo "missing from docs/governance.md: ${missing}"; false; }
}

# ---------------------------------------------------------------------------
# Required documents (OSPS-SA-01.01, OSPS-SA-03.01, OSPS-DO-06.01) exist and are reachable.
# ---------------------------------------------------------------------------

@test "docs/architecture.md: the five diagrams, linked from security and operations" {
  local -r doc="${REPO_ROOT}/docs/architecture.md"
  [ "$(grep -c '^```mermaid$' "${doc}")" -eq 5 ]
  grep -q 'architecture.md' "${REPO_ROOT}/docs/security.md"
  grep -q 'architecture.md' "${REPO_ROOT}/docs/operations.md"
}

@test "docs/threat-model.md: every component has a threat row; linked; on the release checklist" {
  local -r doc="${REPO_ROOT}/docs/threat-model.md"
  grep -q '^| ID | Component | Threat | Likelihood | Impact | Mitigation | Residual |$' "${doc}"
  local c missing=''
  for c in TLS Authn Authz ACME Proxy Admin 'Remote log sink' 'FIPS monitor' 'Release pipeline'; do
    grep -qE "^\| [A-Z]+-[0-9]+ \| ${c} \|" "${doc}" || missing+="${c}; "
  done
  [ -z "${missing}" ] || { echo "no threat row for: ${missing}"; false; }
  grep -q 'threat-model.md' "${REPO_ROOT}/SECURITY.md"
  grep -q 'threat-model.md' "${REPO_ROOT}/docs/security.md"
  grep -q 'threat-model.md' "${REPO_ROOT}/docs/release-policy.md"
}

@test "docs/dependencies.md: the policy states the license allow list make lint-licenses enforces" {
  local -r doc="${REPO_ROOT}/docs/dependencies.md"
  local allowed
  allowed="$(grep -v '^#' "${REPO_ROOT}/.standards/config/go-licenses.txt" | paste -sd, -)"
  [ -n "${allowed}" ]
  local l missing=''
  for l in ${allowed//,/ }; do
    grep -qF "\`${l}\`" "${doc}" || missing+="${l} "
  done
  [ -z "${missing}" ] || { echo "licenses missing from the policy: ${missing}"; false; }
  grep -q 'dependencies.md' "${REPO_ROOT}/docs/CONTRIBUTING.md"
  grep -q 'dependencies.md' "${REPO_ROOT}/SECURITY.md"
}

# ---------------------------------------------------------------------------
# Runbooks: docs/runbooks/, one template, every command exercised by a test.
# ---------------------------------------------------------------------------

@test "runbooks: each exists with the template's sections and is indexed in operations.md" {
  local r s missing=''
  for r in cert-expiry fips-monitor-trip opa-unavailable authn-reject-storm prestop-timeout \
    remote-log-sink-down h3-quic-fallback log-rotation; do
    local f="${REPO_ROOT}/docs/runbooks/${r}.md"
    [ -f "${f}" ] || { missing+="${r}: file; "; continue; }
    for s in 'Symptoms' 'Immediate triage' 'Root causes' 'Resolution' 'Escalation'; do
      grep -q "^## ${s}\$" "${f}" || missing+="${r}: ${s}; "
    done
    grep -q "runbooks/${r}.md" "${REPO_ROOT}/docs/operations.md" || missing+="${r}: index; "
  done
  [ -z "${missing}" ] || { echo "${missing}"; false; }
}

# Every command in a runbook's sh blocks is curl on a keel endpoint or a signal to keel, and that
# endpoint or signal is exercised by the test suites.
@test "runbooks: every command is exercised by a test" {
  local line bad=''
  local -r tests="${REPO_ROOT}/tests/integrity.bats ${REPO_ROOT}/tests/compose/compose_test.go ${REPO_ROOT}/pkg/core/server_signals_internal_test.go"
  while IFS= read -r line; do
    case "${line}" in
      curl\ *)
        local path
        path="$(sed -nE 's#.*https?://[^/ ]+(/[A-Za-z0-9/_-]*).*#\1#p' <<< "${line}")"
        [ -n "${path}" ] && grep -qF "${path}" ${tests} || bad+="${line}; " ;;
      kill\ -HUP\ *) grep -qE 'kill -HUP|SIGHUP' ${tests} || bad+="${line}; " ;;
      kill\ -USR2\ *) grep -qE 'kill -USR2|SIGUSR2' ${tests} || bad+="${line}; " ;;
      *) bad+="${line}; " ;;
    esac
  done < <(awk '/^```sh$/ { on = 1; next } /^```$/ { on = 0 } on' "${REPO_ROOT}"/docs/runbooks/*.md)
  [ -z "${bad}" ] || { echo "not exercised: ${bad}"; false; }
}

# ---------------------------------------------------------------------------
# run_bounded: tests/helpers/wait.bash, a time limit with no timeout(1), which macOS does not ship.
# ---------------------------------------------------------------------------

@test "run_bounded: passes through the command's status and output, without waiting for the limit" {
  load helpers/wait
  local -r start="${SECONDS}"
  run run_bounded 20 bash -c 'printf out; exit 3'
  [ "${status}" -eq 3 ]
  [ "${output}" = 'out' ]
  [ $(( SECONDS - start )) -lt 10 ]
}

@test "run_bounded: stops a command that overruns, with status 124" {
  load helpers/wait
  local -r start="${SECONDS}"
  run run_bounded 1 sleep 30
  [ "${status}" -eq 124 ]
  [ $(( SECONDS - start )) -lt 10 ]
}

@test "Makefile: keel's old target names are gone" {
  while read -r old new; do
    run make -n --no-print-directory "${old}"
    [ "${status}" -ne 0 ] || { echo "${old} still exists"; return 1; }
    [[ "${output}" == *'No rule to make target'* ]]
  done <<'RENAMED'
all build-all
bats-example test-example-artifact
bats-integrity test-integrity-artifact
certs-image build-certs-image
ci-build-example build-example
ci-smoke-windows test-smoke-windows
container-deploy deploy-container
container-host-network setup-container-host-network
container-setup setup-container
container-stack-test test-container-stack
container-stack-test-keep test-container-stack-keep
container-teardown teardown-container
container-test test-container
coverage-report report-coverage
dev-image-publish-local publish-dev-image-local
fresh-repo create-fresh-repo
gen-certs generate-certs
gen-schema generate-schema
k8s-cluster-test test-k8s-cluster
k8s-helm-deploy deploy-k8s-helm
k8s-kind-load load-k8s-kind
k8s-kind-setup setup-k8s-kind
k8s-kind-teardown teardown-k8s-kind
k8s-stack-down teardown-k8s-stack
k8s-stack-up deploy-k8s-stack
keel-schema-regen generate-schema-staged
max build-max
max-no-fips build-max-no-fips
min build-min
native-build-local build-native-local
native-integrity-local test-native-integrity-local
release-binaries build-release-binaries
release-ci run-release-ci
release-images-local publish-release-images-local
roadmap-issues create-roadmap-issues
rulesets-apply apply-rulesets
rulesets-check check-rulesets
size-report report-size
RENAMED
}

# container_stubs: a fake container CLI that logs every call and reports NODE_STATE from inspect, a
# fake make and brew that log their calls, and a fake scripts/dev/run.sh (KEELCORE_RUN_SH) that names a
# tool image without fetching it.
container_stubs() {
  mkdir -p "${BATS_TEST_TMPDIR}/bin"
  cat > "${BATS_TEST_TMPDIR}/bin/container" <<STUB
#!/usr/bin/env bash
printf '%s\n' "\$*" >> "${BATS_TEST_TMPDIR}/container.log"
case "\${1}" in
  inspect) [ -n "\${NODE_STATE:-}" ] || exit 1; printf '[{"status":{"state":"%s"}}]\n' "\${NODE_STATE}" ;;
esac
exit 0
STUB
  cat > "${BATS_TEST_TMPDIR}/bin/run.sh" <<'STUB'
#!/usr/bin/env bash
[ "${*: -1}" != '--image-ref' ] || printf 'ghcr.io/keelcore/keel-stub:ctx-0123456789abcdef\n'
STUB
  export KEELCORE_RUN_SH="${BATS_TEST_TMPDIR}/bin/run.sh"
  local tool
  for tool in make brew; do
    printf '#!/usr/bin/env bash\nprintf "%%s\\n" "$*" >> "%s/%s.log"\n' "${BATS_TEST_TMPDIR}" "${tool}" \
      > "${BATS_TEST_TMPDIR}/bin/${tool}"
  done
  chmod +x "${BATS_TEST_TMPDIR}/bin/"*
}

@test "container scripts: none calls cluster load-image or finds a node with container ls" {
  run grep -nE 'cluster load-image|container (ls|list)( |$)' "${REPO_ROOT}"/scripts/container/*.sh
  [ "${status}" -eq 1 ] || { echo "${output}"; return 1; }
}

@test "setup.sh: installs container with make setup-apple-container; the cluster shares the image store" {
  container_stubs
  PATH="${BATS_TEST_TMPDIR}/bin:${PATH}" run bash "${REPO_ROOT}/scripts/container/setup.sh"
  [ "${status}" -eq 0 ] || { echo "${output}"; return 1; }
  grep -q 'setup-apple-container' "${BATS_TEST_TMPDIR}/make.log"
  [ ! -e "${BATS_TEST_TMPDIR}/brew.log" ]
  grep -qE '^cluster create .*--shared-image-store' "${BATS_TEST_TMPDIR}/container.log"
}

@test "deploy.sh: a node is running when container inspect says so; a stopped one names setup.sh" {
  container_stubs
  NODE_STATE=running PATH="${BATS_TEST_TMPDIR}/bin:${PATH}" run bash "${REPO_ROOT}/scripts/container/deploy.sh"
  [ "${status}" -eq 0 ] || { echo "${output}"; return 1; }
  grep -q '^inspect keel-k8s' "${BATS_TEST_TMPDIR}/container.log"
  NODE_STATE=stopped PATH="${BATS_TEST_TMPDIR}/bin:${PATH}" run bash "${REPO_ROOT}/scripts/container/deploy.sh"
  [ "${status}" -eq 1 ]
  [[ "${output}" == *"'keel-k8s' is not running. Run: scripts/container/setup.sh"* ]]
}

@test "report-size.sh: finishes cleanly and removes its symbol table" {
  local -r script="${REPO_ROOT}/scripts/build/report-size.sh"
  local -r work="${BATS_TEST_TMPDIR}/work"
  mkdir -p "${work}/bin"
  cat > "${work}/bin/go" <<'GO'
#!/usr/bin/env bash
case "${1}" in
  build) touch 'dist/keel-max-debug' ;;
  tool) printf '1000 64 T main.main\n' ;;
esac
GO
  chmod +x "${work}/bin/go"
  local before
  before="$(ls /tmp/keel_nm_* 2>/dev/null || true)"
  cd "${work}"
  PATH="${work}/bin:${PATH}" run bash "${script}"
  [ "${status}" -eq 0 ]
  [ "$(ls /tmp/keel_nm_* 2>/dev/null || true)" = "${before}" ]
}

@test "make build-dev-image: puts keel's dev image on this machine, pulled or built, never pushed" {
  run make -n build-dev-image
  [ "${status}" -eq 0 ]
  [[ "${output}" == *'bash scripts/dev/build-image.local.sh'* ]] || false
  grep -qF 'run.sh" --fetch-image' scripts/dev/build-image.local.sh
  [ -z "$(grep -vE '^#' scripts/dev/build-image.local.sh | grep -E 'push')" ]
}

@test "make setup-kind: installs kind in the dev image, for amd64 and arm64, and creates no cluster" {
  run make -n --no-print-directory setup-kind
  [ "${status}" -eq 0 ]
  [[ "${output}" == *'bash scripts/dev/run.sh bash scripts/ci/setup-kind.sh'* ]] || false
  grep -qE "^readonly KIND_SHA256_AMD64='[0-9a-f]{64}'" scripts/ci/setup-kind.sh
  grep -qE "^readonly KIND_SHA256_ARM64='[0-9a-f]{64}'" scripts/ci/setup-kind.sh
  run grep -nE 'kind create cluster|^[[:space:]]*sudo ' scripts/ci/setup-kind.sh
  [ "${status}" -eq 1 ] || { echo "${output}"; return 1; }
}

# gosec_stub: a fake gosec that logs its arguments and prints GOSEC_ISSUES (gosec's JSON issue list).
gosec_stub() {
  mkdir -p "${BATS_TEST_TMPDIR}/bin"
  cat > "${BATS_TEST_TMPDIR}/bin/gosec" <<STUB
#!/usr/bin/env bash
printf '%s\n' "\$*" >> "${BATS_TEST_TMPDIR}/gosec.log"
printf '{"Issues": %s}\n' "\${GOSEC_ISSUES:-[]}"
STUB
  chmod +x "${BATS_TEST_TMPDIR}/bin/gosec"
}

# site_issues: one gosec issue per entry of config/gosec-sites.local.txt, at the line its code is on.
site_issues() {
  local rule path code why line
  while IFS=$'\t' read -r rule path code why; do
    line="$(grep -nF -- "${code}" "${path}" | head -1 | cut -d: -f1)"
    jq -nc --arg r "${rule}" --arg f "${REPO_ROOT}/${path}" --arg l "${line}" '{rule_id: $r, file: $f, line: $l, details: "x"}'
  done < <(grep -vE '^[[:space:]]*(#|$)' config/gosec-sites.local.txt) | jq -sc .
}

@test "make lint-gosec-local: gosec skips every submodule folder, starting with .standards" {
  run make -n --no-print-directory lint-gosec-local
  [[ "${output}" == *'bash scripts/dev/run.sh bash scripts/lint/lint-gosec.local.sh'* ]] || false
  gosec_stub
  GOSEC_ISSUES="$(site_issues)" PATH="${BATS_TEST_TMPDIR}/bin:${PATH}" run bash scripts/lint/lint-gosec.local.sh
  [ "${status}" -eq 0 ] || { echo "${output}"; return 1; }
  grep -q -- '-exclude-dir=.standards' "${BATS_TEST_TMPDIR}/gosec.log"
}

@test "lint-gosec.local.sh: a listed site passes; the same rule anywhere else in the file fails" {
  gosec_stub
  local issues
  issues="$(site_issues | jq -c --arg f "${REPO_ROOT}/pkg/core/mw/oidc.go" '. + [{rule_id: "G407", file: $f, line: "1", details: "x"}]')"
  GOSEC_ISSUES="${issues}" PATH="${BATS_TEST_TMPDIR}/bin:${PATH}" run bash scripts/lint/lint-gosec.local.sh
  [ "${status}" -eq 1 ]
  [[ "${output}" == *'G407 pkg/core/mw/oidc.go:1'* ]] || false
}

@test "lint-gosec.local.sh: a listed site gosec no longer reports fails, naming the entry" {
  gosec_stub
  GOSEC_ISSUES="$(site_issues | jq -c '.[1:]')" PATH="${BATS_TEST_TMPDIR}/bin:${PATH}" run bash scripts/lint/lint-gosec.local.sh
  [ "${status}" -eq 1 ]
  [[ "${output}" == *'stale site'*'G407'*'pkg/core/mw/oidc.go'* ]] || false
}

# ---------------------------------------------------------------------------
# Shared env imports (#37): every scripts/container script that reads a shared
# name imports it with declare -rx, and all of them give it one default.
# ---------------------------------------------------------------------------
@test "make: every target that runs a tool image or Apple container ensures the container runtime (#42)" {
  local missing
  missing="$(cd "${REPO_ROOT}" && make -pRrq -f Makefile : 2> /dev/null | awk '
    /^[a-zA-Z0-9_.-]+:([^=]|$)/ { target = $1; sub(/:$/, "", target); deps = $0; next }
    /^$/ { target = "" }
    /^\t/ && target != "" && target != "ensure-container" \
      && /scripts\/dev\/|scripts\/container\/|update-pins\.sh|publish-release-images\.sh/ \
      && deps !~ /\|.*[[:space:]]ensure-container([[:space:]]|$)/ { print target; target = "" }
  ' | sort -u)"
  [ -z "${missing}" ] || {
    echo "targets without | ensure-container:"
    echo "${missing}"
    return 1
  }
}

@test "scripts/container imports KEEL_CLUSTER_NAME and KEEL_TOOLBOX_NAME with one default each" {
  local name script defaults
  for name in KEEL_CLUSTER_NAME KEEL_TOOLBOX_NAME; do
    defaults=''
    for script in $(grep -l "${name}" "${REPO_ROOT}"/scripts/container/*.sh); do
      grep -qE "^declare -rx ${name}=\"\\\$\{${name}:-[^}]*\}\"$" "${script}" || {
        echo "${script}: ${name} is not imported with declare -rx"
        return 1
      }
      defaults+="$(sed -nE "s/^declare -rx ${name}=\"\\\$\{${name}:-([^}]*)\}\"$/\1/p" "${script}")"$'\n'
    done
    [ "$(printf '%s' "${defaults}" | sort -u | wc -l | tr -d ' ')" = '1' ] || {
      echo "${name} defaults differ:"
      printf '%s' "${defaults}" | sort | uniq -c
      return 1
    }
  done
}

@test "mockupstream: part of keel's module, so it builds from this tree, tagged or not" {
  [ ! -e "${REPO_ROOT}/tests/mockupstream/go.mod" ]
  [ ! -e "${REPO_ROOT}/tests/mockupstream/go.sum" ]
  [ ! -e "${REPO_ROOT}/tests/mockupstream/vendor" ]
  run grep -F '`github.com/keelcore/keel`' "${REPO_ROOT}/docs/current_pins.md"
  [ "${status}" -eq 1 ] || { echo "${output}"; return 1; }
}

@test "mockupstream Containerfile: packages the prebuilt mock, with no compiler stage" {
  local -r file="${REPO_ROOT}/tests/mockupstream/Containerfile"
  [ "$(grep -cE '^FROM ' "${file}")" -eq 1 ]
  grep -qxF 'FROM scratch' "${file}"
  grep -qxF 'COPY mockupstream /mockupstream' "${file}"
}

@test "build-mockupstream-image.sh: compiles the mock from this tree; the image builds from its binary alone" {
  mkdir -p "${BATS_TEST_TMPDIR}/bin"
  printf '#!/usr/bin/env bash\nprintf "%%s\\n" "$*" >> "%s/docker.log"\n' "${BATS_TEST_TMPDIR}" \
    > "${BATS_TEST_TMPDIR}/bin/docker"
  chmod +x "${BATS_TEST_TMPDIR}/bin/docker"
  PATH="${BATS_TEST_TMPDIR}/bin:${PATH}" run bash "${REPO_ROOT}/scripts/build/build-mockupstream-image.sh"
  [ "${status}" -eq 0 ] || { echo "${output}"; return 1; }
  [ -x "${REPO_ROOT}/target/mockupstream/mockupstream" ]
  [ "$(ls -A "${REPO_ROOT}/target/mockupstream")" = 'mockupstream' ]
  grep -qxF "build -t keel-mockupstream:test -f ${REPO_ROOT}/tests/mockupstream/Containerfile ${REPO_ROOT}/target/mockupstream" \
    "${BATS_TEST_TMPDIR}/docker.log"
}

@test "the compose and Apple container stacks build the mock with build-mockupstream-image.sh" {
  grep -qF 'bash "${REPO_ROOT}/scripts/build/build-mockupstream-image.sh"' "${REPO_ROOT}/scripts/test/test-compose.sh"
  grep -qF 'KEEL_IMAGE_TOOL=container bash "${REPO_ROOT}/scripts/build/build-mockupstream-image.sh"' \
    "${REPO_ROOT}/scripts/container/test-container-stack.sh"
  local service
  service="$(awk '/^  mockupstream:$/ { on = 1; next } on && /^  [a-z]/ { exit } on' \
    "${REPO_ROOT}/tests/compose/docker-compose.test.yaml")"
  [[ "${service}" != *'build:'* ]] || false
  [[ "${service}" == *'pull_policy: never'* ]] || false
}

@test "the compose and k8s stacks set only KEEL_ env names docs/config-reference.md documents" {
  local name missing=''
  for name in $(grep -ohE 'KEEL_[A-Z0-9_]+' "${REPO_ROOT}/tests/compose/docker-compose.test.yaml" \
    "${REPO_ROOT}/tests/compose/k8s-stack.yaml" | sort -u); do
    grep -qF "\`${name}\`" "${REPO_ROOT}/docs/config-reference.md" || missing="${missing} ${name}"
  done
  [ -z "${missing}" ] || { echo "undocumented, so keel ignores them:${missing}"; return 1; }
}
