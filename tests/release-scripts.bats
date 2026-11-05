#!/usr/bin/env bats
# tests/release-scripts.bats
# keel's release scripts run their slow work concurrently (run_parallel is tested in .standards). Each script runs
# here against stubs of docker, git, gh and the dev image, in a throwaway tree. A concurrency test
# uses a barrier: every job waits until all of them have started, which only concurrent jobs reach.

setup_file() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/.." && pwd)"
  export REPO_ROOT
}

setup() {
  WORK="${BATS_TEST_TMPDIR}/work"
  STATE="${BATS_TEST_TMPDIR}/state"
  export WORK STATE
  mkdir -p "${WORK}/scripts/release" "${WORK}/scripts/lib" "${WORK}/scripts/dev" "${WORK}/scripts/build" \
    "${WORK}/dist" "${WORK}/bin" "${STATE}/started"
  cp "${REPO_ROOT}"/scripts/release/*.sh "${WORK}/scripts/release/"
  cp "${REPO_ROOT}"/scripts/build/ci_*.sh "${WORK}/scripts/build/"
  cp "${REPO_ROOT}/scripts/lib/parallel.sh" "${WORK}/scripts/lib/"
  stub_tools
  : > "${STATE}/calls"
  export PATH="${WORK}/bin:${PATH}"
  export GITHUB_TOKEN=token GITHUB_ACTOR=actor GH_TOKEN=token
  export GITHUB_OUTPUT="${STATE}/output"
  export BARRIER_TIMEOUT=30
  local flavor arch
  for flavor in max min fips; do
    for arch in amd64 arm64; do
      printf 'binary' > "${WORK}/dist/keel-${flavor}-linux-${arch}"
      printf 'bundle' > "${WORK}/dist/keel-${flavor}-linux-${arch}.bundle"
    done
  done
  printf 'sums' > "${WORK}/dist/SHA256SUMS"
  printf 'bundle' > "${WORK}/dist/SHA256SUMS.bundle"
  printf '{}' > "${WORK}/dist/keel.openvex.json"
  cd "${WORK}"
}

# stub_tools writes the fake commands. Every call lands in STATE/calls. barrier NAME COUNT waits
# until COUNT distinct names have started. FAIL_ON makes a call matching its regex fail.
stub_tools() {
  cat > "${WORK}/bin/stub-lib" <<'LIB'
record() { printf '%s\n' "$*" >> "${STATE}/calls"; }
maybe_fail() { if [ -n "${FAIL_ON:-}" ] && [[ "$*" =~ ${FAIL_ON} ]]; then exit 1; fi; }
barrier() {
  : > "${STATE}/started/${1//[^A-Za-z0-9._-]/_}"
  local waited=0
  until [ "$(find "${STATE}/started" -type f | wc -l)" -ge "${2}" ]; do
    sleep 0.1
    waited=$(( waited + 1 ))
    [ "${waited}" -lt $(( BARRIER_TIMEOUT * 10 )) ] || { printf 'barrier timed out\n' >&2; exit 1; }
  done
}
LIB
  cat > "${WORK}/bin/git" <<'GIT'
#!/usr/bin/env bash
case "${1}" in
  describe) printf 'v1.2.3\n' ;;
  log) printf '1700000000\n' ;;
  rev-parse) printf 'abc123\n' ;;
esac
GIT
  cat > "${WORK}/bin/docker" <<'DOCKER'
#!/usr/bin/env bash
source "${WORK}/bin/stub-lib"
record "docker $*"
maybe_fail "docker $*"
case "${1}" in
  build)
    [ -z "${DOCKER_BUILD_BARRIER:-}" ] || barrier "build-$(sed -E 's/.*--tag ([^ ]+).*/\1/' <<< "$*")" "${DOCKER_BUILD_BARRIER}"
    ;;
  inspect)
    ref="${*: -1}"
    printf 'ghcr.io/keelcore/keel@sha256:%s\n' "${ref##*:}"
    ;;
  save) : > "${3}" ;;
  buildx)
    case "${3}" in
      inspect)
        if [ "${5:-}" = '--raw' ]; then
          printf '{"manifests":[{"platform":{"os":"linux"},"digest":"sha256:a"},{"platform":{"os":"linux"},"digest":"sha256:b"}]}\n'
        else
          case "${4}" in
            *-min) printf '{"digest":"sha256:min"}\n' ;;
            *-fips) printf '{"digest":"sha256:fips"}\n' ;;
            *) printf '{"digest":"sha256:max"}\n' ;;
          esac
        fi
        ;;
    esac
    ;;
esac
DOCKER
  cat > "${WORK}/bin/gh" <<'GH'
#!/usr/bin/env bash
source "${WORK}/bin/stub-lib"
record "gh $*"
maybe_fail "gh $*"
GH
  cat > "${WORK}/scripts/dev/run.sh" <<'RUN'
#!/usr/bin/env bash
source "${WORK}/bin/stub-lib"
record "dev GRYPE_DB_AUTO_UPDATE=${GRYPE_DB_AUTO_UPDATE:-} $*"
maybe_fail "dev $*"
if [ -n "${VERIFY_BARRIER:-}" ] && [[ "$*" =~ cosign\.sh\ verify\ .*@sha256:(max|min|fips)$ ]]; then
  barrier "verify-${BASH_REMATCH[1]}" "${VERIFY_BARRIER}"
fi
if [ -n "${BLOB_BARRIER:-}" ] && [[ "$*" =~ verify-blob ]]; then
  barrier "blob-${*##*/}" "${BLOB_BARRIER}"
fi
RUN
  printf '#!/usr/bin/env bash\n' > "${WORK}/scripts/build/certs-image.sh"
  chmod +x "${WORK}/bin/git" "${WORK}/bin/docker" "${WORK}/bin/gh" "${WORK}/scripts/dev/run.sh" \
    "${WORK}/scripts/build/certs-image.sh" "${WORK}/scripts/release/docker.sh"
}

calls() { grep -E "${1}" "${STATE}/calls"; }
call_count() { grep -cE "${1}" "${STATE}/calls" || true; }

# --- docker.sh ---------------------------------------------------------------------------------

@test "docker.sh: all six images build concurrently" {
  export DOCKER_BUILD_BARRIER=6
  run ./scripts/release/docker.sh
  [ "${status}" -eq 0 ]
}

@test "docker.sh: each index holds its amd64 and arm64 images, in that order" {
  run ./scripts/release/docker.sh
  [ "${status}" -eq 0 ]
  calls 'imagetools create --tag ghcr.io/keelcore/keel:v1.2.3 ghcr.io/keelcore/keel@sha256:v1.2.3-amd64 ghcr.io/keelcore/keel@sha256:v1.2.3-arm64$'
  calls 'imagetools create --tag ghcr.io/keelcore/keel:v1.2.3-min ghcr.io/keelcore/keel@sha256:v1.2.3-min-amd64 ghcr.io/keelcore/keel@sha256:v1.2.3-min-arm64$'
  calls 'imagetools create --tag ghcr.io/keelcore/keel:v1.2.3-fips ghcr.io/keelcore/keel@sha256:v1.2.3-fips-amd64 ghcr.io/keelcore/keel@sha256:v1.2.3-fips-arm64$'
}

@test "docker.sh: records each flavor's index digest for the workflow" {
  run ./scripts/release/docker.sh
  [ "${status}" -eq 0 ]
  grep -qx 'max_digest=sha256:max' "${GITHUB_OUTPUT}"
  grep -qx 'min_digest=sha256:min' "${GITHUB_OUTPUT}"
  grep -qx 'fips_digest=sha256:fips' "${GITHUB_OUTPUT}"
}

@test "docker.sh: each image is signed and attested, by digest" {
  run ./scripts/release/docker.sh
  [ "${status}" -eq 0 ]
  [ "$(call_count 'cosign\.sh sign --yes ')" -eq 9 ]
  [ "$(call_count 'cosign\.sh attest --yes --predicate .*spdx\.json')" -eq 6 ]
  [ "$(call_count 'cosign\.sh attest --yes --predicate .*openvex')" -eq 3 ]
}

@test "docker.sh: the grype database is fetched once, before any scan, and scans leave it alone" {
  run ./scripts/release/docker.sh
  [ "${status}" -eq 0 ]
  [ "$(call_count 'grype db update')" -eq 1 ]
  local update first_scan
  update="$(grep -n 'grype db update' "${STATE}/calls" | head -1 | cut -d: -f1)"
  first_scan="$(grep -n 'grype sbom:' "${STATE}/calls" | head -1 | cut -d: -f1)"
  [ "${update}" -lt "${first_scan}" ]
  [ "$(call_count 'GRYPE_DB_AUTO_UPDATE=false grype sbom:')" -eq 6 ]
}

@test "docker.sh: a scan finding fails the release before that image is pushed" {
  export FAIL_ON='grype sbom:dist/sbom-min-arm64'
  run ./scripts/release/docker.sh
  [ "${status}" -ne 0 ]
  [ "$(call_count 'docker push .*-min-arm64$')" -eq 0 ]
  [ "$(call_count 'imagetools create --tag ghcr.io/keelcore/keel:v1.2.3-min ')" -eq 0 ]
}

@test "docker.sh: a failed push fails the release" {
  export FAIL_ON='docker push .*-fips-amd64$'
  run ./scripts/release/docker.sh
  [ "${status}" -ne 0 ]
  [ "$(call_count 'imagetools create --tag ghcr.io/keelcore/keel:v1.2.3-fips ')" -eq 0 ]
}

@test "docker.sh: a failure in max leaves the aliases where they were" {
  export FAIL_ON='grype sbom:dist/sbom-max-amd64'
  run ./scripts/release/docker.sh
  [ "${status}" -ne 0 ]
  [ "$(call_count 'imagetools create --tag ghcr.io/keelcore/keel:(v1|v1\.2|latest) ')" -eq 0 ]
}

@test "docker.sh: aliases move after the max index is signed" {
  run ./scripts/release/docker.sh
  [ "${status}" -eq 0 ]
  local signed alias
  signed="$(grep -n 'cosign\.sh sign --yes ghcr.io/keelcore/keel@sha256:max$' "${STATE}/calls" | head -1 | cut -d: -f1)"
  alias="$(grep -n 'imagetools create --tag ghcr.io/keelcore/keel:latest' "${STATE}/calls" | head -1 | cut -d: -f1)"
  [ -n "${signed}" ]
  [ "${signed}" -lt "${alias}" ]
}

# --- verify-release.sh -------------------------------------------------------------------------

verify_env() {
  export MAX_DIGEST=sha256:max MIN_DIGEST=sha256:min FIPS_DIGEST=sha256:fips
}

@test "verify-release.sh: the three indexes verify concurrently" {
  verify_env
  export VERIFY_BARRIER=3
  run bash scripts/release/verify-release.sh
  [ "${status}" -eq 0 ]
}

@test "verify-release.sh: the blobs verify concurrently" {
  verify_env
  export BLOB_BARRIER=7
  run bash scripts/release/verify-release.sh
  [ "${status}" -eq 0 ]
}

@test "verify-release.sh: checks every index, arch image, alias, blob and the chart" {
  verify_env
  run bash scripts/release/verify-release.sh
  [ "${status}" -eq 0 ]
  [[ "${output}" == *'Release v1.2.3 verified'* ]]
  [ "$(call_count 'cosign\.sh verify-attestation .*--type openvex ')" -eq 3 ]
  [ "$(call_count 'gh attestation verify oci://')" -eq 3 ]
  [ "$(call_count 'cosign\.sh verify-attestation .*spdx\.dev')" -eq 6 ]
  [ "$(call_count 'cosign\.sh verify-blob ')" -eq 7 ]
  [ "$(call_count 'gh attestation verify dist/')" -eq 6 ]
  [ "$(call_count 'imagetools inspect ghcr.io/keelcore/keel:(v1|v1\.2|latest) ')" -eq 3 ]
  [ "$(call_count 'cosign\.sh verify .*charts/keel:1\.2\.3$')" -eq 1 ]
}

@test "verify-release.sh: one failed check fails the whole verification" {
  verify_env
  local check
  for check in 'cosign\.sh verify .*@sha256:fips$' 'gh attestation verify dist/keel-min-linux-arm64 ' \
    'cosign\.sh verify .*charts/keel' 'imagetools inspect ghcr.io/keelcore/keel:latest '; do
    export FAIL_ON="${check}"
    run bash scripts/release/verify-release.sh
    [ "${status}" -ne 0 ] || { printf 'passed with %s failing\n' "${check}" >&3; false; }
    [[ "${output}" != *'Release v1.2.3 verified'* ]]
  done
}

@test "verify-release.sh: an alias on the wrong digest fails" {
  export MAX_DIGEST=sha256:other MIN_DIGEST=sha256:min FIPS_DIGEST=sha256:fips
  run bash scripts/release/verify-release.sh
  [ "${status}" -ne 0 ]
}

# --- build-binaries.sh -------------------------------------------------------------------------

# stub_go writes a go whose builds record their GOARCH in the output file.
stub_go() {
  cat > "${WORK}/bin/go" <<'GO'
#!/usr/bin/env bash
source "${WORK}/bin/stub-lib"
record "go GOARCH=${GOARCH:-} GOFIPS140=${GOFIPS140:-} $*"
maybe_fail "go GOARCH=${GOARCH:-} $*"
case "${1}" in
  env)
    case "${2}" in
      GOARCH) printf '%s\n' "${GOARCH:-amd64}" ;;
      GOHOSTARCH) printf 'amd64\n' ;;
      *) printf 'linux\n' ;;
    esac
    ;;
  version) printf 'go version go1.27 linux/amd64\n' ;;
  build)
    out=''
    while [ "${#}" -gt 0 ]; do
      if [ "${1}" = '-o' ]; then out="${2}"; fi
      shift
    done
    [ -z "${BUILD_BARRIER:-}" ] || barrier "${out//\//_}" "${BUILD_BARRIER}"
    printf 'GOARCH=%s\n' "${GOARCH:-}" > "${out}"
    ;;
esac
GO
  chmod +x "${WORK}/bin/go"
}

@test "build-binaries.sh: all six binaries build concurrently" {
  stub_go
  rm -rf dist/*
  export BUILD_BARRIER=6
  run bash scripts/release/build-binaries.sh
  [ "${status}" -eq 0 ]
}

@test "build-binaries.sh: each flavor and architecture lands under its release name, built for that architecture" {
  stub_go
  rm -rf dist/*
  run bash scripts/release/build-binaries.sh
  [ "${status}" -eq 0 ]
  local flavor arch
  for flavor in min max fips; do
    for arch in amd64 arm64; do
      grep -qx "GOARCH=${arch}" "dist/keel-${flavor}-linux-${arch}"
    done
  done
  [ "$(find dist -mindepth 1 | wc -l)" -eq 6 ]
}

@test "build-binaries.sh: the FIPS build carries the module, and its test runs on the host architecture only" {
  stub_go
  rm -rf dist/*
  run bash scripts/release/build-binaries.sh
  [ "${status}" -eq 0 ]
  [ "$(call_count 'go GOARCH=(amd64|arm64) GOFIPS140=[^ ]+ build ')" -eq 2 ]
  [ "$(call_count 'go GOARCH=amd64 GOFIPS140=[^ ]+ test ')" -eq 1 ]
  [ "$(call_count 'go GOARCH=arm64 GOFIPS140=[^ ]+ test ')" -eq 0 ]
}

@test "build-binaries.sh: one failed build fails the step" {
  stub_go
  rm -rf dist/*
  export FAIL_ON='go GOARCH=arm64 build .*keel-min'
  run bash scripts/release/build-binaries.sh
  [ "${status}" -ne 0 ]
}

# --- metadata.sh -------------------------------------------------------------------------------

stub_metadata() {
  cat > "${WORK}/scripts/release/sbom.sh" <<'SBOM'
#!/usr/bin/env bash
source "${WORK}/bin/stub-lib"
record "sbom $*"
maybe_fail "sbom $*"
[ -z "${METADATA_BARRIER:-}" ] || barrier sbom "${METADATA_BARRIER}"
SBOM
  cat > "${WORK}/scripts/release/vex.sh" <<'VEX'
#!/usr/bin/env bash
source "${WORK}/bin/stub-lib"
record "vex $*"
maybe_fail "vex $*"
[ -z "${METADATA_BARRIER:-}" ] || barrier vex "${METADATA_BARRIER}"
: > dist/keel.openvex.json
VEX
  rm -f dist/keel.openvex.json
  export TAG=v1.2.3
}

@test "metadata.sh: the SBOMs and OpenVEX generate concurrently" {
  stub_metadata
  export METADATA_BARRIER=2
  run bash scripts/release/metadata.sh
  [ "${status}" -eq 0 ]
}

@test "metadata.sh: the SBOMs cover the six binaries and nothing else" {
  stub_metadata
  rm -f dist/*.bundle
  printf '{}' > dist/keel.openvex.json
  run bash scripts/release/metadata.sh
  [ "${status}" -eq 0 ]
  calls '^sbom --out-dir dist --name keel --version v1\.2\.3 --license Apache-2\.0 dist/keel-'
  [ "$(calls '^sbom ' | tr ' ' '\n' | grep -c '^dist/keel-')" -eq 6 ]
  ! calls '^sbom ' | grep -q 'openvex'
  calls '^vex --out-dir dist$'
}

@test "metadata.sh: one failure fails the step" {
  stub_metadata
  export FAIL_ON='vex '
  run bash scripts/release/metadata.sh
  [ "${status}" -ne 0 ]
}

# --- upload.sh ---------------------------------------------------------------------------------

stub_release_gh() {
  cat > "${WORK}/bin/gh" <<'GH'
#!/usr/bin/env bash
source "${WORK}/bin/stub-lib"
record "gh $*"
maybe_fail "gh $*"
if [ -n "${UPLOAD_BARRIER:-}" ] && [ "${2:-}" = upload ]; then
  barrier "$(basename "${4}")" "${UPLOAD_BARRIER}"
fi
GH
  export RELEASE_TAG=v1.2.3
}

@test "upload.sh: assets upload concurrently, one call per asset" {
  stub_release_gh
  export UPLOAD_BARRIER=8
  run bash scripts/release/upload.sh
  [ "${status}" -eq 0 ]
  [ "$(call_count '^gh release upload v1\.2\.3 [^ ]+ --clobber$')" -ge 8 ]
}

@test "upload.sh: a failed asset fails the step" {
  stub_release_gh
  export FAIL_ON='gh release upload v1\.2\.3 dist/SHA256SUMS '
  run bash scripts/release/upload.sh
  [ "${status}" -ne 0 ]
}

@test "upload.sh: PROVENANCE_BUNDLE may name the directory an artifact downloaded into" {
  stub_release_gh
  mkdir provenance
  printf '{"dsseEnvelope":{"payload":"x"}}\n' > provenance/whatever.jsonl
  export PROVENANCE_BUNDLE=provenance
  run bash scripts/release/upload.sh
  [ "${status}" -eq 0 ]
  grep -q '"payload":"x"' dist/keel.intoto.jsonl
}

@test "upload.sh --publish: the image SBOMs attach concurrently, and publishing comes last" {
  stub_release_gh
  local flavor arch
  for flavor in max min fips; do
    for arch in amd64 arm64; do
      printf '{}' > "dist/sbom-${flavor}-${arch}.spdx.json"
    done
  done
  export UPLOAD_BARRIER=6
  run bash scripts/release/upload.sh --publish
  [ "${status}" -eq 0 ]
  tail -1 "${STATE}/calls" | grep -q 'gh release edit v1\.2\.3 --draft=false --latest'
}

@test "upload.sh --publish: a failed SBOM upload leaves the release a draft" {
  stub_release_gh
  local flavor arch
  for flavor in max min fips; do
    for arch in amd64 arm64; do
      printf '{}' > "dist/sbom-${flavor}-${arch}.spdx.json"
    done
  done
  export FAIL_ON='gh release upload v1\.2\.3 dist/sbom-min-arm64'
  run bash scripts/release/upload.sh --publish
  [ "${status}" -ne 0 ]
  [ "$(call_count 'gh release edit')" -eq 0 ]
}

# --- release.yml -------------------------------------------------------------------------------

# job_block JOB: the workflow's lines for one job.
job_block() {
  awk -v job="  ${1}:" '$0 == job { on = 1; next } on && /^  [a-z-]+:$/ { exit } on' \
    "${REPO_ROOT}/.github/workflows/release.yml"
}

# job_needs JOB: the jobs JOB waits for, one per line.
job_needs() {
  job_block "${1}" | sed -nE 's/^    needs: *\[?([^]]*)\]?$/\1/p' | tr -d ' ' | tr ',' '\n'
}

# needs_closure JOB: JOB and every job it waits for.
needs_closure() {
  local dep
  printf '%s\n' "${1}"
  for dep in $(job_needs "${1}"); do
    needs_closure "${dep}"
  done
}

# job_permissions JOB: the job's permissions as "name: level", sorted.
job_permissions() {
  job_block "${1}" | awk '/^    permissions:$/ { on = 1; next } on && /^      / { sub(/^ +/, ""); print; next } on { exit }' | sort
}

@test "release.yml: approval comes first: one job holds the release environment, and all publishing waits for it" {
  [ "$(grep -c '^    environment: release$' "${REPO_ROOT}/.github/workflows/release.yml")" -eq 1 ]
  job_block approve | grep -qx '    environment: release'
  local job
  for job in sign-blobs attest-blobs images helm upload image-provenance verify publish; do
    needs_closure "${job}" | grep -qx 'approve' || { printf '%s skips approval\n' "${job}" >&3; false; }
  done
}

@test "release.yml: signing, attesting, images and the chart run side by side" {
  local job
  for job in sign-blobs attest-blobs images helm; do
    [ "$(job_needs "${job}")" = 'approve' ]
  done
}

@test "release.yml: the assets upload after signing and attesting; image provenance after the images" {
  needs_closure upload | grep -qx 'sign-blobs'
  needs_closure upload | grep -qx 'attest-blobs'
  needs_closure image-provenance | grep -qx 'images'
  job_block image-provenance | grep -qx '        flavor: \[max, min, fips\]'
}

@test "release.yml: verification waits for everything, and the release publishes only after it" {
  local job
  for job in images helm upload image-provenance; do
    needs_closure verify | grep -qx "${job}"
  done
  [ "$(job_needs publish)" = 'verify' ]
}

@test "release.yml: each job holds only the permissions it uses" {
  [ "$(job_permissions build)" = 'contents: read' ]
  [ "$(job_permissions approve)" = 'contents: read' ]
  [ "$(job_permissions sign-blobs | paste -sd, -)" = 'contents: read,id-token: write' ]
  [ "$(job_permissions attest-blobs | paste -sd, -)" = 'attestations: write,contents: read,id-token: write' ]
  [ "$(job_permissions images | paste -sd, -)" = 'contents: read,id-token: write,packages: write' ]
  [ "$(job_permissions helm | paste -sd, -)" = 'contents: read,id-token: write,packages: write' ]
  [ "$(job_permissions upload)" = 'contents: write' ]
  [ "$(job_permissions image-provenance | paste -sd, -)" = 'attestations: write,contents: read,id-token: write,packages: write' ]
  [ "$(job_permissions verify | paste -sd, -)" = 'attestations: read,contents: read,packages: read' ]
  [ "$(job_permissions publish)" = 'contents: write' ]
}

@test "release.yml: image provenance logs in to the registry before pushing the attestation" {
  local login attest
  login="$(job_block image-provenance | grep -n 'make release-ghcr-login' | cut -d: -f1)"
  attest="$(job_block image-provenance | grep -n 'attest-build-provenance' | cut -d: -f1)"
  [ -n "${login}" ]
  [ "${login}" -lt "${attest}" ]
  job_block image-provenance | grep -q 'GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}'
}

@test "ghcr-login.sh: logs in with the job's token and fails without one" {
  run env -u GITHUB_TOKEN bash scripts/release/ghcr-login.sh
  [ "${status}" -ne 0 ]
  export GITHUB_TOKEN=token GITHUB_ACTOR=actor
  run bash scripts/release/ghcr-login.sh
  [ "${status}" -eq 0 ]
  calls '^docker login ghcr\.io --username actor --password-stdin$'
}

@test "release.yml: artifacts that lose their mode are made executable before the images use them" {
  local chmod_line docker_line
  chmod_line="$(job_block images | grep -n 'make dist-chmod' | cut -d: -f1)"
  docker_line="$(job_block images | grep -n 'make release-docker' | cut -d: -f1)"
  [ -n "${chmod_line}" ]
  [ "${chmod_line}" -lt "${docker_line}" ]
}
