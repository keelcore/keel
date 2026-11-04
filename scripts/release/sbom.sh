#!/usr/bin/env bash
# scripts/release/sbom.sh
# One SPDX SBOM per published artifact, describing exactly what ships: its
# name, version and contents (packages, and the Go modules compiled into a Go
# binary).
#
# Usage: scripts/release/sbom.sh --out-dir DIR --name NAME --version VERSION [--license SPDX] TARGET [TARGET ...]
#   --license  the license of the release's own package, recorded when its metadata has none
#   --tools TSV  (image targets) tools the image installs that syft cannot identify (a static
#              binary, a script install): rows of name, version, purl, download URL; each one not
#              already cataloged is added as a package the image CONTAINS
#   TARGET   a package archive (.tgz, .tar.gz, .whl, .zip: unpacked, then scanned),
#            a binary or other file (scanned as a file), or an image by digest
#            (REPO@sha256:...: scanned from the registry — the build environment)
#   Output:  DIR/<target basename>.spdx.json for each TARGET (an image:
#            DIR/<repo name>.spdx.json)
#
# Requires: syft, jq and unzip on PATH (the dev image carries them).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

declare OUT_DIR='' NAME='' VERSION='' LICENSE='' TOOLS=''
declare -a TARGETS=()

function main() {
  exec 5>&1
  parse_args "$@"
  validate_env
  mkdir -p "${OUT_DIR}"
  local target
  for target in "${TARGETS[@]}"; do
    sbom_for "${target}"
  done
  log "✅ ${#TARGETS[@]} SBOM(s) in ${OUT_DIR}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/sbom.log' >&5
}

function parse_args() {
  while [ "${#}" -gt 0 ]; do
    case "${1}" in
      --out-dir) OUT_DIR="${2:-}"; shift 2 ;;
      --name) NAME="${2:-}"; shift 2 ;;
      --version) VERSION="${2:-}"; shift 2 ;;
      --license) LICENSE="${2:-}"; shift 2 ;;
      --tools) TOOLS="${2:-}"; shift 2 ;;
      -*) usage ;;
      *) TARGETS+=("${1}"); shift ;;
    esac
  done
  [ -n "${OUT_DIR}" ] && [ -n "${NAME}" ] && [ -n "${VERSION}" ] && [ "${#TARGETS[@]}" -gt 0 ] || usage
}

function usage() {
  log '❌ Usage: sbom.sh --out-dir DIR --name NAME --version VERSION [--license SPDX] TARGET [TARGET ...]'
  exit 1
}

function validate_env() {
  local tool
  for tool in syft jq unzip; do
    command -v "${tool}" > /dev/null 2>&1 && continue
    log "❌ ${tool} not found on PATH (the dev image carries it: make release-sbom)"
    exit 1
  done
}

# sbom_for TARGET: the SBOM of one published artifact (or build-environment image).
function sbom_for() {
  local -r target="${1}"
  local source out tmp='' scan_dir='' catalogers=''
  case "${target}" in
    *@sha256:*)
      source="registry:${target}"
      out="${OUT_DIR}/$(basename "${target%@*}" | cut -d: -f1).spdx.json" ;;
    *.whl | *.zip | *.tgz | *.tar.gz)
      tmp="$(mktemp -d)"
      case "${target}" in
        *.whl | *.zip) unzip -q "${target}" -d "${tmp}/content" ;;
        *) mkdir -p "${tmp}/content" && tar -xzf "${target}" -C "${tmp}/content" ;;
      esac
      scan_dir="${tmp}/content"
      select_catalogers "${tmp}"
      source="dir:${scan_dir}"
      out="${OUT_DIR}/$(basename "${target}").spdx.json" ;;
    *)
      [ -f "${target}" ] || { log "❌ ${target} not found"; exit 1; }
      source="file:${target}"
      out="${OUT_DIR}/$(basename "${target}").spdx.json" ;;
  esac
  local -a args=(scan "${source}" --source-name "${NAME}" --source-version "${VERSION}" --output "spdx-json=${out}")
  [ -z "${catalogers}" ] || args+=(--override-default-catalogers "${catalogers}")
  syft "${args[@]}"
  [ -z "${tmp}" ] || rm -rf "${tmp}"
  complete_self "${out}"
  [ -z "${TOOLS}" ] || [[ "${target}" != *@sha256:* ]] || add_tools "${out}" "${TOOLS}"
  log "✅ ${out} ($(jq '.packages | length' "${out}") packages)"
}

# select_catalogers TMP: catalog a package archive by its own ecosystem's metadata only — the
# package and its declared dependencies, never files it merely carries (another tool's
# lockfile in a source archive is not a dependency). Sets catalogers and scan_dir.
function select_catalogers() {
  local -r tmp="${1}"
  local pkginfo
  if [ -f "${tmp}/content/package/package.json" ]; then
    catalogers='javascript-package-cataloger'
  elif compgen -G "${tmp}/content/*.dist-info" > /dev/null; then
    catalogers='python-installed-package-cataloger'
  elif pkginfo="$(compgen -G "${tmp}/content/*/PKG-INFO" | head -1)" && [ -n "${pkginfo}" ]; then
    # An sdist's PKG-INFO is core metadata, read where an installed package keeps it.
    mkdir -p "${tmp}/meta/sdist.egg-info"
    cp "${pkginfo}" "${tmp}/meta/sdist.egg-info/PKG-INFO"
    scan_dir="${tmp}/meta"
    catalogers='python-installed-package-cataloger'
  elif [ -n "$(find "${tmp}/content" -maxdepth 4 -name go.mod -print -quit)" ]; then
    # A Go module zip: <module path>@<version>/go.mod.
    catalogers='go-module-file-cataloger'
  fi
}

# complete_self SBOM: the release's own package gets the release version where the cataloger
# could not read one (a Go main module: UNKNOWN), and LICENSE where none was declared; the
# document root (the released artifact itself) carries LICENSE too.
function complete_self() {
  local -r sbom="${1}"
  local gover="v${VERSION#v}"
  jq --arg v "${VERSION}" --arg gv "${gover}" --arg lic "${LICENSE}" '
    .packages |= map(
      if .versionInfo == "UNKNOWN" then
        .versionInfo = (if ((.externalRefs // []) | map(.referenceLocator) | any(startswith("pkg:golang/"))) then $gv else $v end)
        | .externalRefs = ((.externalRefs // []) | map(
            if .referenceType == "purl" and (.referenceLocator | test("@") | not)
            then .referenceLocator += "@" + (if (.referenceLocator | startswith("pkg:golang/")) then $gv else $v end)
            else . end))
        | (if $lic != "" and (.licenseDeclared // "NOASSERTION") == "NOASSERTION" then .licenseDeclared = $lic else . end)
      elif (.SPDXID | startswith("SPDXRef-DocumentRoot")) and $lic != "" and (.licenseDeclared // "NOASSERTION") == "NOASSERTION" then
        .licenseDeclared = $lic
      else . end)' "${sbom}" > "${sbom}.tmp"
  mv "${sbom}.tmp" "${sbom}"
}

# add_tools SBOM TSV: every tool row (name, version, purl, URL) whose name no cataloged package
# carries becomes a package the document root CONTAINS.
function add_tools() {
  local -r sbom="${1}" tsv="${2}"
  jq --rawfile rows "${tsv}" '
    ([.packages[].name | ascii_downcase]) as $known
    | ([.packages[] | select(.SPDXID | startswith("SPDXRef-DocumentRoot")) | .SPDXID][0]) as $root
    | ($rows | split("\n") | map(select(length > 0) | split("\t"))
        | map(select(.[0] as $n | $known | any(contains($n | ascii_downcase)) | not))) as $add
    | .packages += ($add | map({
        name: .[0], SPDXID: ("SPDXRef-Package-tool-" + .[0]), versionInfo: .[1], supplier: "NOASSERTION",
        downloadLocation: .[3], filesAnalyzed: false, licenseConcluded: "NOASSERTION",
        licenseDeclared: "NOASSERTION", copyrightText: "NOASSERTION",
        externalRefs: [{referenceCategory: "PACKAGE-MANAGER", referenceType: "purl", referenceLocator: .[2]}],
        comment: "Installed at image build time by a pinned setup script (version and checksum or commit committed in the repository)"}))
    | .relationships += ($add | map({spdxElementId: $root, relationshipType: "CONTAINS",
        relatedSpdxElement: ("SPDXRef-Package-tool-" + .[0])}))' "${sbom}" > "${sbom}.tmp"
  mv "${sbom}.tmp" "${sbom}"
  log "   + $(jq -r '[.packages[] | select(.SPDXID | startswith("SPDXRef-Package-tool-")) | .name] | join(", ")' "${sbom}")"
}

main "$@"
