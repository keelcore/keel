#!/usr/bin/env bash
# update-pins.local.sh
# keel hook for the canonical scripts/release/update-pins.sh
# (`make update-pins`). Project-owned, never canonical.
#
# Usage: update-pins.local.sh pins|holds
#        update-pins.local.sh verify KIND NAME FILE PLATFORM
#        update-pins.local.sh combined|cleanup PLATFORM
#        update-pins.local.sh apply ROOT KIND NAME CURRENT CANDIDATE LOCATIONS
#   pins      records for pins the generic discovery cannot resolve:
#             kind<TAB>name<TAB>file:line<TAB>current<TAB>source
#   holds     inputs that are not external pins, with the reason:
#             name-regex<TAB>reason
#   verify    make targets that exercise one pin (empty: the driver's default)
#   combined  make targets for the combined run: builds first, then the full unit
#             and integration set, including the container stack
#   cleanup   remove the toolbox the combined run started
#   apply     write a pin the driver cannot (exit 0 done, 3 not handled)

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly TOOLBOX='scripts/container/toolbox/Containerfile'
readonly SETUP_HELM='scripts/ci/setup-helm.sh'
# The combined run's toolbox mounts the throwaway worktree, so it never shares a
# name with the developer's keel-toolbox (which mounts the working tree).
readonly PINS_TOOLBOX='keel-toolbox-pins'
# The verification cluster: its own name, so a pin check never touches the developer's keel-k8s.
readonly PINS_CLUSTER='keel-k8s-pins'

function main() {
  validate_args "${@:-}"
  local -r cmd="${1}"
  shift
  case "${cmd}" in
    pins) pins ;;
    holds) holds ;;
    verify) verify "$@" ;;
    combined) combined "$@" ;;
    cleanup) cleanup ;;
    apply) apply "$@" ;;
  esac
}

function validate_args() {
  case "${1:-}" in
    pins | holds | verify | combined | cleanup | apply) ;;
    *)
      printf 'Usage: update-pins.local.sh pins|holds|verify|combined|cleanup|apply ...\n' >&2
      exit 1 ;;
  esac
}

# toolbox_arg NAME: "<line>\t<value>" of `ARG NAME=value` in the toolbox Containerfile.
function toolbox_arg() {
  grep -nE "^ARG ${1}=" "${TOOLBOX}" | sed -E "s/^([0-9]+):ARG ${1}=(.*)$/\1\t\2/"
}

# pins: the toolbox downloads kubectl and helm from their own CDNs (dl.k8s.io,
# get.helm.sh), which publish the same releases as their GitHub projects;
# setup-helm.sh installs the same helm on Linux CI runners.
function pins() {
  local line value
  IFS=$'\t' read -r line value < <(toolbox_arg KUBECTL_VERSION)
  printf 'tool\tKUBECTL\t%s:%s\t%s\tgithub:kubernetes/kubernetes\n' "${TOOLBOX}" "${line}" "${value}"
  IFS=$'\t' read -r line value < <(toolbox_arg HELM_VERSION)
  printf 'tool\tHELM\t%s:%s\t%s\tgithub:helm/helm\n' "${TOOLBOX}" "${line}" "${value}"
  line="$(grep -nE "^readonly HELM_VERSION=" "${SETUP_HELM}" | cut -d: -f1)"
  value="$(sed -nE "s/^readonly HELM_VERSION='([^']+)'\$/\1/p" "${SETUP_HELM}")"
  printf 'tool\tHELM\t%s:%s\t%s\tgithub:helm/helm\n' "${SETUP_HELM}" "${line}" "${value}"
}

function holds() {
  printf '%s\t%s\n' \
    '^(docker\.io/library/)?keel(-mockupstream)?:test' 'built locally from this repo by the test stacks' \
    '^example\.invalid/' 'placeholder image in a Helm policy test fixture' \
    '^REQUIRED_GO$' 'a version-prefix check on the installed Go, not a pin (go.mod pins the toolchain)' \
    '^KUBE_MIN$' 'oldest Kubernetes API the chart supports; raised deliberately, not on release'
}

# verify KIND NAME FILE PLATFORM: each tool's install plus the target that runs it.
function verify() {
  local -r kind="${1}" name="${2}" platform="${4}"
  case "${kind}:${name}" in
    gomod:*) printf 'build-min build-max test' ;;
    tool:GOSEC) printf 'setup-gosec lint-gosec-local' ;;
    gotool:*/govulncheck) printf 'setup-govulncheck lint-vuln' ;;
    tool:ACTIONLINT) printf 'setup-actionlint lint-workflows' ;;
    gotool:*/go-licenses*) printf 'setup-go-licenses lint-licenses' ;;
    gotool:*/staticcheck) printf 'setup-staticcheck' ;;
    gotool:*/pebble) printf 'setup-pebble build-min build-max test' ;;
    tool:KUBESCAPE) printf 'setup-kubescape lint-helm-policy' ;;
    tool:ZIZMOR) printf 'setup-zizmor lint-workflows' ;;
    tool:KUBECONFORM) printf 'setup-kubeconform lint-helm-validate' ;;
    tool:GOTESTSUM) printf 'setup-gotestsum test-unit' ;;
    # Rebuilding the toolbox image verifies the new version's checksums; helm also installs in the dev image.
    tool:KUBECTL)
      if [ "${platform}" = 'Darwin' ]; then printf 'KEEL_CLUSTER_NAME=%s KEEL_TOOLBOX_NAME=%s setup-container' "${PINS_CLUSTER}" "${PINS_TOOLBOX}"; fi ;;
    tool:HELM)
      printf 'setup-helm lint-helm'
      if [ "${platform}" = 'Darwin' ]; then printf ' KEEL_CLUSTER_NAME=%s KEEL_TOOLBOX_NAME=%s setup-container' "${PINS_CLUSTER}" "${PINS_TOOLBOX}"; fi ;;
  esac
}

# combined PLATFORM: builds, lint, audit, unit + integrity BATS, then the
# integration stack — Apple container locally, Docker Compose on Linux.
function combined() {
  if [ "${1}" = 'Darwin' ]; then
    printf 'KEEL_CLUSTER_NAME=%s KEEL_TOOLBOX_NAME=%s build-min build-max lint audit test setup-container test-container-stack' "${PINS_CLUSTER}" "${PINS_TOOLBOX}"
  else
    printf 'build-min build-max lint audit test test-compose'
  fi
}

# cleanup: tear down everything a verification started (cluster, helm release, toolbox) —
# nothing it starts outlives the run.
function cleanup() {
  if command -v container > /dev/null 2>&1; then
    KEEL_CLUSTER_NAME="${PINS_CLUSTER}" KEEL_TOOLBOX_NAME="${PINS_TOOLBOX}" bash scripts/container/teardown.sh > /dev/null 2>&1 || true
    container rm -f "${PINS_TOOLBOX}" > /dev/null 2>&1 || true
  fi
}

# apply ROOT KIND NAME CURRENT CANDIDATE LOCATIONS: kubectl and helm in the
# toolbox Containerfile (and helm in setup-helm.sh), with the checksums their
# CDNs publish.
function apply() {
  local -r root="${1}" kind="${2}" name="${3}" current="${4}" cand="${5}"
  [ "${kind}" = 'tool' ] || return 3
  local -r file="${root}/${TOOLBOX}"
  local arch sum
  case "${name}" in
    KUBECTL | HELM) ;;
    *) return 3 ;;
  esac
  replace_arg "${file}" "${name}_VERSION" "${current}" "${cand}"
  if [ "${name}" = 'HELM' ]; then
    CUR="${current}" CAND="${cand}" perl -pi -e 's/^(readonly HELM_VERSION=\x27)\Q$ENV{CUR}\E(\x27)$/$1$ENV{CAND}$2/' "${root}/${SETUP_HELM}"
    grep -q "^readonly HELM_VERSION='${cand}'$" "${root}/${SETUP_HELM}" || { printf 'HELM_VERSION not updated in %s\n' "${SETUP_HELM}"; return 1; }
  fi
  for arch in amd64 arm64; do
    if [ "${name}" = 'KUBECTL' ]; then
      sum="$(curl -fsSL --retry 3 "https://dl.k8s.io/release/${cand}/bin/linux/${arch}/kubectl.sha256")"
    else
      sum="$(curl -fsSL --retry 3 "https://get.helm.sh/helm-${cand}-linux-${arch}.tar.gz.sha256sum" | awk '{print $1}')"
    fi
    [[ "${sum}" =~ ^[0-9a-f]{64}$ ]] || { printf 'no published checksum for %s %s %s\n' "${name}" "${cand}" "${arch}"; return 1; }
    ARG="${name}_SHA256_$(tr '[:lower:]' '[:upper:]' <<< "${arch}")" SUM="${sum}" \
      perl -pi -e 's/^(ARG \Q$ENV{ARG}\E=)[0-9a-f]{64}$/$1$ENV{SUM}/' "${file}"
    grep -q "=${sum}$" "${file}" || { printf 'checksum not written for %s %s\n' "${name}" "${arch}"; return 1; }
    if [ "${name}" = 'HELM' ]; then
      ARG="HELM_SHA256_$(tr '[:lower:]' '[:upper:]' <<< "${arch}")" SUM="${sum}" \
        perl -pi -e 's/^(readonly \Q$ENV{ARG}\E=\x27)[0-9a-f]{64}(\x27)$/$1$ENV{SUM}$2/' "${root}/${SETUP_HELM}"
      grep -q "='${sum}'$" "${root}/${SETUP_HELM}" || { printf 'checksum not written to %s for %s\n' "${SETUP_HELM}" "${arch}"; return 1; }
    fi
  done
}

function replace_arg() {
  local -r file="${1}" arg="${2}" current="${3}" cand="${4}"
  ARG="${arg}" CUR="${current}" CAND="${cand}" perl -pi -e 's/^(ARG \Q$ENV{ARG}\E=)\Q$ENV{CUR}\E$/$1$ENV{CAND}/' "${file}"
  grep -q "^ARG ${arg}=${cand}$" "${file}" || { printf '%s not updated in %s\n' "${arg}" "${file}"; return 1; }
}

main "${@:-}"
