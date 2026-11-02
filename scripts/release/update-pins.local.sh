#!/usr/bin/env bash
# update-pins.local.sh
# keel hook for the canonical scripts/release/update-pins.sh
# (`make update-pins`). Project-owned, never canonical.
#
# Usage: update-pins.local.sh pins|holds
#   pins   records for pins the generic discovery cannot resolve:
#          kind<TAB>name<TAB>file:line<TAB>current<TAB>source
#   holds  inputs that are not external pins, with the reason:
#          name-regex<TAB>reason

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly TOOLBOX='scripts/container/toolbox/Containerfile'

function main() {
  validate_args "${@:-}"
  case "${1}" in
    pins) pins ;;
    holds) holds ;;
  esac
}

function validate_args() {
  if [ "${#}" -ne 1 ] || [[ "${1}" != 'pins' && "${1}" != 'holds' ]]; then
    printf 'Usage: update-pins.local.sh pins|holds\n' >&2
    exit 1
  fi
}

# toolbox_arg NAME: "<line>\t<value>" of `ARG NAME=value` in the toolbox Containerfile.
function toolbox_arg() {
  grep -nE "^ARG ${1}=" "${TOOLBOX}" | sed -E "s/^([0-9]+):ARG ${1}=(.*)$/\1\t\2/"
}

# pins: the toolbox downloads kubectl and helm from their own CDNs (dl.k8s.io,
# get.helm.sh), which publish the same releases as their GitHub projects.
function pins() {
  local line value
  IFS=$'\t' read -r line value < <(toolbox_arg KUBECTL_VERSION)
  printf 'tool\tKUBECTL\t%s:%s\t%s\tgithub:kubernetes/kubernetes\n' "${TOOLBOX}" "${line}" "${value}"
  IFS=$'\t' read -r line value < <(toolbox_arg HELM_VERSION)
  printf 'tool\tHELM\t%s:%s\t%s\tgithub:helm/helm\n' "${TOOLBOX}" "${line}" "${value}"
}

function holds() {
  printf '%s\t%s\n' \
    '^(docker\.io/library/)?keel(-mockupstream)?:test' 'built locally from this repo by the test stacks' \
    '^example\.invalid/' 'placeholder image in a Helm policy test fixture' \
    '^REQUIRED_GO$' 'a version-prefix check on the installed Go, not a pin (go.mod pins the toolchain)' \
    '^KUBE_MIN$' 'oldest Kubernetes API the chart supports; raised deliberately, not on release'
}

main "${@:-}"
