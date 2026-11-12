#!/usr/bin/env bats
# tests/ensure-keelcore.bats
# keelcore's own releases take no update-pins cooldown: scripts/release/ensure-keelcore.sh moves each
# github.com/keelcore/* module go.mod requires to its latest release, and each `FROM golang:` base image
# to the Go release go.mod's toolchain names. `make update-pins` runs it first (make ensure-keelcore).
# It edits configuration only: no container, build or test runs. It runs here in a throwaway git tree
# against stubs of curl (the Go module proxy and Docker Hub) and go; make and the dev image (run.sh) are
# stubbed only to show they are never called.

setup_file() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/.." && pwd)"
  export REPO_ROOT
}

setup() {
  WORK="${BATS_TEST_TMPDIR}/work"
  STATE="${BATS_TEST_TMPDIR}/state"
  export WORK STATE
  mkdir -p "${WORK}/scripts/release" "${WORK}/scripts/dev" "${WORK}/vendor" "${WORK}/build" "${WORK}/bin" "${STATE}"
  cp "${REPO_ROOT}/scripts/release/ensure-keelcore.sh" "${WORK}/scripts/release/"
  printf 'module github.com/keelcore/keel\n\ngo 1.27\n\ntoolchain go1.27.4\n\nrequire (\n\tgithub.com/golang-jwt/jwt/v5 v5.3.1\n\tgithub.com/keelcore/knobs v0.8.7\n)\n\nrequire github.com/keelcore/other v1.0.0 // indirect\n' \
    > "${WORK}/go.mod"
  printf 'sums\n' > "${WORK}/go.sum"
  printf '# github.com/keelcore/knobs v0.8.7\n' > "${WORK}/vendor/modules.txt"
  printf 'FROM golang:1.27.4@%s AS builder\n' "${OLD}" > "${WORK}/build/Dockerfile"
  printf 'FROM golang:1.27.4-alpine@%s\n' "${OLD}" > "${WORK}/build/Containerfile"
  stub_tools
  : > "${STATE}/calls"
  export PATH="${WORK}/bin:${PATH}"
  export LATEST_knobs=v0.8.7 LATEST_other=v1.0.0
  cd "${WORK}"
  git init -q .
  git add -A
  git -c user.email=test@example.invalid -c user.name=test commit -qm fixture
}

OLD="sha256:$(printf 'a%.0s' {1..64})"
NEW="sha256:$(printf 'b%.0s' {1..64})"
NEW_ALPINE="sha256:$(printf 'c%.0s' {1..64})"
export OLD NEW NEW_ALPINE

# stub_tools writes the fake commands; every call lands in STATE/calls. curl answers the module proxy's
# version list with LATEST_<last path element> between an older release and a newer prerelease, and a
# Docker Hub manifest HEAD for golang:1.27.5 with NEW (NEW_ALPINE for -alpine). go prints noise, rewrites
# go.mod for `go get M@V`, vendors a new file for `go mod vendor`, and fails a call matching GO_FAILS.
stub_tools() {
  cat > "${WORK}/bin/curl" <<'CURL'
#!/usr/bin/env bash
printf 'curl %s\n' "$*" >> "${STATE}/calls"
url="${*: -1}"
case "${url}" in
  https://proxy.golang.org/*)
    mod="${url%/@v/list}"
    var="LATEST_${mod##*/}"
    printf 'v0.0.1\n%s\nv99.0.0-rc.1\n' "${!var}" ;;
  https://auth.docker.io/*) printf '{"token":"t"}\n' ;;
  */manifests/1.27.5) printf 'HTTP/1.1 200 OK\r\nDocker-Content-Digest: %s\r\n\r\n' "${NEW}" ;;
  */manifests/1.27.5-alpine) printf 'HTTP/1.1 200 OK\r\nDocker-Content-Digest: %s\r\n\r\n' "${NEW_ALPINE}" ;;
  *) exit 22 ;;
esac
CURL
  cat > "${WORK}/bin/go" <<'GO'
#!/usr/bin/env bash
printf 'go %s\n' "$*" >> "${STATE}/calls"
printf 'go: downloading noise\n'
printf 'go: stderr noise\n' >&2
if [ -n "${GO_FAILS:-}" ] && [[ "$*" =~ ${GO_FAILS} ]]; then exit 1; fi
case "$1 ${2:-}" in
  'get '*)
    spec="$2"
    sed -i.bak -E "s#^([[:space:]]*)${spec%@*} v[^ ]+#\\1${spec%@*} ${spec#*@}#" go.mod
    rm -f go.mod.bak ;;
  'mod vendor') : > vendor/new-file.go ;;
esac
GO
  printf '#!/usr/bin/env bash\nprintf "%%s %%s\\n" "$(basename "$0")" "$*" >> "${STATE}/calls"\n' > "${WORK}/bin/make"
  cp "${WORK}/bin/make" "${WORK}/scripts/dev/run.sh"
  chmod +x "${WORK}/bin/curl" "${WORK}/bin/go" "${WORK}/bin/make" "${WORK}/scripts/dev/run.sh"
}

# toolchain VERSION: go.mod's toolchain line names goVERSION, committed.
toolchain() {
  sed -i.bak -E "s/^toolchain .*/toolchain go${1}/" go.mod
  rm -f go.mod.bak
  git -c user.email=test@example.invalid -c user.name=test commit -qam toolchain
}

@test "ensure-keelcore: a keelcore module behind its latest release is reported, with no cooldown, and nothing is written" {
  export LATEST_knobs=v0.8.8
  run bash scripts/release/ensure-keelcore.sh
  [ "${status}" -eq 0 ]
  [[ "${output}" == *'github.com/keelcore/knobs v0.8.7 → v0.8.8'* ]] || false
  grep -qxF $'\tgithub.com/keelcore/knobs v0.8.7' go.mod
  ! grep -qE '^(go|run.sh|make) ' "${STATE}/calls" || false
}

@test "ensure-keelcore: only github.com/keelcore/* modules are looked up, indirect ones included" {
  run bash scripts/release/ensure-keelcore.sh
  [ "${status}" -eq 0 ]
  [ "$(grep -c 'proxy.golang.org' "${STATE}/calls")" -eq 2 ]
  grep -q 'proxy.golang.org/github.com/keelcore/knobs/@v/list' "${STATE}/calls"
  grep -q 'proxy.golang.org/github.com/keelcore/other/@v/list' "${STATE}/calls"
  [[ "${output}" == *'github.com/keelcore/other v1.0.0 current'* ]] || false
}

@test "ensure-keelcore: --apply with everything current writes nothing" {
  run bash scripts/release/ensure-keelcore.sh --apply
  [ "${status}" -eq 0 ]
  ! grep -qE '^(go|run.sh|make) ' "${STATE}/calls" || false
  [ -z "$(git status --porcelain)" ]
}

@test "ensure-keelcore: --apply moves a keelcore module with go get, tidy and vendor, quietly, and builds nothing" {
  export LATEST_knobs=v0.8.8
  run bash scripts/release/ensure-keelcore.sh --apply
  [ "${status}" -eq 0 ]
  grep -qxF $'\tgithub.com/keelcore/knobs v0.8.8' go.mod
  [ "$(grep '^go ' "${STATE}/calls")" = $'go get github.com/keelcore/knobs@v0.8.8\ngo mod tidy\ngo mod vendor' ]
  [[ "${output}" != *'noise'* ]] || false
  ! grep -qE '^(run.sh|make) ' "${STATE}/calls" || false
}

@test "ensure-keelcore: a golang base image behind go.mod's toolchain is reported, and nothing is written" {
  toolchain 1.27.5
  run bash scripts/release/ensure-keelcore.sh
  [ "${status}" -eq 0 ]
  [[ "${output}" == *'build/Dockerfile:1 golang:1.27.4 → golang:1.27.5 (go.mod toolchain go1.27.5)'* ]] || false
  [[ "${output}" == *'build/Containerfile:1 golang:1.27.4-alpine → golang:1.27.5-alpine (go.mod toolchain go1.27.5)'* ]] || false
  [ -z "$(git status --porcelain)" ]
}

@test "ensure-keelcore: --apply moves each golang base image to the toolchain's release by digest, and builds nothing" {
  toolchain 1.27.5
  run bash scripts/release/ensure-keelcore.sh --apply
  [ "${status}" -eq 0 ]
  grep -qxF "FROM golang:1.27.5@${NEW} AS builder" build/Dockerfile
  grep -qxF "FROM golang:1.27.5-alpine@${NEW_ALPINE}" build/Containerfile
  ! grep -qE '^(go|run.sh|make) ' "${STATE}/calls" || false
}

@test "ensure-keelcore: --apply fails, writing nothing, when the toolchain's golang image has no digest" {
  toolchain 1.27.9
  run bash scripts/release/ensure-keelcore.sh --apply
  [ "${status}" -ne 0 ]
  [[ "${output}" == *'no digest for golang:1.27.9'* ]] || false
  [ -z "$(git status --porcelain)" ]
}

@test "ensure-keelcore: --apply restores every file it changed when go fails, and names its log" {
  export LATEST_knobs=v0.8.8 GO_FAILS='^mod vendor'
  toolchain 1.27.5
  run bash scripts/release/ensure-keelcore.sh --apply
  [ "${status}" -ne 0 ]
  [[ "${output}" == *'restored'* ]] || false
  [[ "${output}" == *'/tmp/keel_ensure_keelcore.log'* ]] || false
  [ -z "$(git status --porcelain)" ]
}

@test "ensure-keelcore: --apply refuses while a file it would change has uncommitted changes" {
  export LATEST_knobs=v0.8.8
  printf 'local\n' >> go.sum
  run bash scripts/release/ensure-keelcore.sh --apply
  [ "${status}" -ne 0 ]
  [[ "${output}" == *'uncommitted'* ]] || false
  ! grep -q '^go ' "${STATE}/calls" || false
  grep -qx 'local' go.sum
}

@test "ensure-keelcore: an unexpected argument is refused" {
  run bash scripts/release/ensure-keelcore.sh --bogus
  [ "${status}" -ne 0 ]
  ! grep -q '^curl' "${STATE}/calls" || false
}

@test "make update-pins runs ensure-keelcore first and passes it APPLY; it needs no container runtime" {
  grep -qxF 'update-pins: | ensure-keelcore' "${REPO_ROOT}/Makefile"
  grep -qxF 'ensure-keelcore:' "${REPO_ROOT}/Makefile"
  grep -qxF $'\tbash scripts/release/ensure-keelcore.sh $(if $(APPLY),--apply,)' "${REPO_ROOT}/Makefile"
  grep -qxF '.PHONY: ensure-keelcore' "${REPO_ROOT}/Makefile"
}
