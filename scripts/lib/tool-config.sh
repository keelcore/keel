#!/usr/bin/env bash
# tool-config.sh
# Tool settings: the default in .standards/config (config/ in the standards repo itself), and a
# config/<name>.local.txt owned by the repository. Source this file; do not execute directly.

function config_default_dir() {
  if [ -d .standards/config ]; then printf '.standards/config'; else printf 'config'; fi
}

# config_lines FILE: FILE's lines without comments or blanks; nothing when it does not exist.
function config_lines() {
  [ -f "${1}" ] || return 0
  sed -e 's/#.*//' -e 's/[[:space:]]*$//' "${1}" | grep -v '^$' || true
}

# config_list NAME: the default list, plus each line of the local file; a -line removes.
function config_list() {
  local -r name="${1}"
  { config_lines "$(config_default_dir)/${name}.txt"; config_lines "config/${name}.local.txt"; } | awk '
    /^-/ { drop[substr($0, 2)] = 1; next }
    { line[++n] = $0 }
    END { for (i = 1; i <= n; i++) if (!(line[i] in drop)) print line[i] }'
}

# config_replace NAME: the local file when the repository has one, else the default.
function config_replace() {
  local -r name="${1}"
  if [ -f "config/${name}.local.txt" ]; then
    config_lines "config/${name}.local.txt"
  else
    config_lines "$(config_default_dir)/${name}.txt"
  fi
}

# project_name: config/project-name.local.txt, else the last part of the Go module path.
function project_name() {
  local name
  name="$(config_replace project-name)"
  [ -n "${name}" ] || name="$(awk '$1 == "module" { print $2; exit }' go.mod 2> /dev/null | sed 's|.*/||')"
  printf '%s' "${name}"
}

# release_files: the release binaries and their by-products in dist/, by config's globs; never a
# signature or the checksums themselves.
function release_files() {
  local glob file
  while IFS= read -r glob; do
    for file in dist/${glob}; do
      [ -f "${file}" ] || continue
      case "${file}" in
        *.bundle | *.sig | *.sigstore.json | */SHA256SUMS*) continue ;;
      esac
      printf '%s\n' "${file}"
    done
  done < <(config_replace release-binaries)
}

# release_repo: owner/name, from the workflow context, else config/release-repo.local.txt.
function release_repo() {
  if [ -n "${GITHUB_REPOSITORY:-}" ]; then
    printf '%s' "${GITHUB_REPOSITORY}"
  else
    config_replace release-repo
  fi
}

# release_identity: the certificate identity of a signature made by this repository's release
# workflow (keyless, GitHub OIDC).
function release_identity() {
  printf '^https://github\\.com/%s/\\.github/workflows/release\\.yml@refs/tags/v' "$(release_repo)"
}

# release_variants: config/release-images.local.txt, one image variant per line: NAME, a tab, its
# tag suffix. The variant with no suffix is the primary, which the aliases point at.
function release_variants() {
  config_replace release-images
}

# chart_name: the name in the first repo-owned Chart.yaml; empty without a chart.
function chart_file() {
  git ls-files '*Chart.yaml' 2> /dev/null | grep -v '^vendor/' | head -1 || true
}

function chart_name() {
  local file
  file="$(chart_file)"
  [ -n "${file}" ] || return 0
  awk '$1 == "name:" { print $2; exit }' "${file}"
}
