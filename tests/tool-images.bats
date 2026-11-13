#!/usr/bin/env bats
# tests/tool-images.bats
# keel's tool images beyond containers/dev — the toolbox and the CA bundle (certs) — are listed in
# config/tool-images.local.txt, so they are named by build context (ctx-<hash>), published, signed and
# attested like the dev image (governance/ci.md, Tool Images), and their build-time packages are bound to
# that image. Their consumers use them by name and never build their own :latest.

setup_file() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/.." && pwd)"
  export REPO_ROOT
}

setup() {
  WORK="${BATS_TEST_TMPDIR}/work"
  STATE="${BATS_TEST_TMPDIR}/state"
  export WORK STATE
  mkdir -p "${WORK}/scripts/build" "${WORK}/scripts/dev" "${WORK}/bin" "${STATE}"
  : > "${STATE}/calls"
}

# stub_certs_tools: run.sh puts the certs image here and names it CERTS_REF; the image tool records each call.
stub_certs_tools() {
  cp "${REPO_ROOT}/scripts/build/build-certs-image.sh" "${WORK}/scripts/build/"
  cat > "${WORK}/scripts/dev/run.sh" <<'RUN'
#!/usr/bin/env bash
printf 'run.sh %s\n' "$*" >> "${STATE}/calls"
[ "${*: -1}" != '--image-ref' ] || printf '%s\n' "${CERTS_REF}"
RUN
  printf '#!/usr/bin/env bash\nprintf "%%s %%s\\n" "$(basename "$0")" "$*" >> "${STATE}/calls"\n' > "${WORK}/bin/docker"
  cp "${WORK}/bin/docker" "${WORK}/bin/container"
  chmod +x "${WORK}/scripts/dev/run.sh" "${WORK}/bin/docker" "${WORK}/bin/container"
  export PATH="${WORK}/bin:${PATH}" CERTS_REF='ghcr.io/keelcore/keel-certs:ctx-0123456789abcdef'
}

@test "tool images: config/tool-images.local.txt lists the toolbox and the certs image" {
  cd "${REPO_ROOT}"
  source scripts/lib/dev-image.sh
  run tool_images
  [ "${status}" -eq 0 ]
  [[ "${output}" == *$'toolbox\tscripts/container/toolbox/Containerfile\tscripts/container/toolbox\tscripts/container/toolbox/Containerfile.image.ref'* ]] || false
  [[ "${output}" == *$'certs\tscripts/deploy/docker/Dockerfile.certs\tscripts/deploy/docker\tscripts/deploy/docker/Dockerfile.certs.image.ref'* ]] || false
}

@test "tool images: each listed image is named by its build context beside its build file" {
  local ref
  for ref in scripts/container/toolbox/Containerfile.image.ref scripts/deploy/docker/Dockerfile.certs.image.ref; do
    [ -f "${REPO_ROOT}/${ref}" ]
    grep -qxE 'ghcr\.io/keelcore/keel-(toolbox|certs):ctx-[0-9a-f]{16}' "${REPO_ROOT}/${ref}"
  done
}

@test "tool images: dev-image.yml is the .standards copy, so it publishes every listed image" {
  cmp "${REPO_ROOT}/.github/workflows/dev-image.yml" "${REPO_ROOT}/.standards/.github/workflows/dev-image.yml"
}

@test "tool images: ci.yml calls dev-image.yml on every push, which publishes them (it has no trigger of its own)" {
  local job
  job="$(awk '/^  dev-image:$/ { on = 1; print; next } on && /^  [^ #]/ { exit } on' "${REPO_ROOT}/.github/workflows/ci.yml")"
  grep -qxF '    uses: ./.github/workflows/dev-image.yml' <<< "${job}"
  grep -qxF "    if: github.event_name != 'pull_request'" <<< "${job}"
  [ "$(awk '/^    permissions:$/ { on = 1; next } on && /^      / { sub(/^ +/, ""); print; next } on { exit }' <<< "${job}" \
    | sort | paste -sd, -)" = 'attestations: write,contents: read,id-token: write,packages: write' ]
}

@test "tool images: the container harness starts the toolbox from its tool image, never a :latest it builds" {
  grep -qF 'bash "${KEELCORE_RUN_SH}" --image toolbox --fetch-image' "${REPO_ROOT}/scripts/container/setup.sh"
  ! grep -rqE "keel-toolbox:latest|container build" "${REPO_ROOT}/scripts/container/setup.sh" || false
}

@test "build-certs-image: puts the certs tool image on this machine as keel-certs:latest and builds nothing" {
  stub_certs_tools
  cd "${WORK}"
  run bash scripts/build/build-certs-image.sh
  [ "${status}" -eq 0 ]
  grep -qx 'run.sh --image certs --fetch-image' "${STATE}/calls"
  grep -qx "docker image tag ${CERTS_REF} keel-certs:latest" "${STATE}/calls"
  ! grep -q ' build' "${STATE}/calls" || false
}

@test "build-certs-image: KEEL_IMAGE_TOOL=container tags it in Apple container's image store" {
  stub_certs_tools
  cd "${WORK}"
  KEEL_IMAGE_TOOL=container run bash scripts/build/build-certs-image.sh
  [ "${status}" -eq 0 ]
  grep -qx "container image tag ${CERTS_REF} keel-certs:latest" "${STATE}/calls"
}

@test "build-certs-image: fails when the certs tool image cannot be fetched" {
  stub_certs_tools
  printf '#!/usr/bin/env bash\nexit 1\n' > "${WORK}/scripts/dev/run.sh"
  cd "${WORK}"
  run bash scripts/build/build-certs-image.sh
  [ "${status}" -ne 0 ]
  ! grep -q 'image tag' "${STATE}/calls" || false
}
