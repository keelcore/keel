#!/usr/bin/env bash
# scripts/admin/apply-rulesets.sh
# Branch protection as code: .github/rulesets/*.json is the source of truth for the repository's
# rulesets (docs/governance.md, Branch Protection).
#
# Usage: apply-rulesets.sh check|contexts|apply
#   contexts  every required status check names a job in .github/workflows (offline)
#   check     contexts, then the live rulesets match the committed JSON. A token without admin
#             rights does not see bypass_actors; the rest is still compared.
#   apply     write the committed JSON to the live rulesets (maintainer: needs GH_TOKEN with admin)
#
# Env: GH_TOKEN (default empty), GITHUB_REPOSITORY (default keelcore/keel), RULESETS_LIVE_DIR
# (default empty; tests: live rulesets read from <dir>/<file>.json instead of the API).

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

declare -rx GH_TOKEN="${GH_TOKEN:-}"
declare -rx GITHUB_REPOSITORY="${GITHUB_REPOSITORY:-keelcore/keel}"
declare -rx RULESETS_LIVE_DIR="${RULESETS_LIVE_DIR:-}"

readonly RULESETS_DIR='.github/rulesets'
readonly WORKFLOWS_DIR='.github/workflows'
readonly API='https://api.github.com'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  case "${1}" in
    contexts) check_contexts ;;
    check) check_contexts && check_live ;;
    apply) apply ;;
  esac
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_rulesets.log' >&5
}

function validate_args() {
  case "${1:-}" in
    check | contexts | apply) ;;
    *)
      log '❌ Usage: apply-rulesets.sh check|contexts|apply'
      exit 1 ;;
  esac
}

# job_patterns: a regex per job in the workflows, from its name, or its key when it has none; a
# matrix expression in a name matches anything.
function job_patterns() {
  awk '
    FNR == 1 { flush(); injobs = 0 }
    /^jobs:/ { injobs = 1; next }
    injobs && /^  [A-Za-z0-9_-]+:[[:space:]]*$/ { flush(); key = $1; sub(/:$/, "", key); next }
    injobs && key != "" && /^    name:/ { line = $0; sub(/^    name:[[:space:]]*/, "", line); name = line; next }
    function flush() { if (key != "") print (name != "" ? name : key); key = ""; name = "" }
    END { flush() }
  ' "${WORKFLOWS_DIR}"/*.yml | sed -E 's/^["'"'"']//; s/["'"'"']$//; s/\$\{\{[^}]*\}\}/@MATRIX@/g; s/[][().*+?^$|\\{}]/\\&/g; s/@MATRIX@/.*/g'
}

# required_contexts: every required status check in the committed rulesets.
function required_contexts() {
  jq -r '.rules[] | select(.type == "required_status_checks") | .parameters.required_status_checks[].context' \
    "${RULESETS_DIR}"/*.json
}

function check_contexts() {
  local patterns context failed=0
  patterns="$(job_patterns)"
  while IFS= read -r context; do
    if ! grep -qxE -f <(printf '%s\n' "${patterns}") <<< "${context}"; then
      log "❌ required check '${context}' names no job in ${WORKFLOWS_DIR}: no pull request could pass"
      failed=1
    fi
  done < <(required_contexts)
  [ "${failed}" -eq 0 ] || return 1
  log '✅ every required check names a workflow job'
}

function api() {
  local auth=()
  [ -z "${GH_TOKEN:-}" ] || auth=(-H "Authorization: Bearer ${GH_TOKEN}")
  curl -fsSL --max-time 20 ${auth[@]+"${auth[@]}"} -H 'Accept: application/vnd.github+json' "$@"
}

function repo() {
  printf '%s' "${GITHUB_REPOSITORY}"
}

# ruleset_id NAME: the live ruleset's id.
function ruleset_id() {
  api "${API}/repos/$(repo)/rulesets" | jq -r --arg n "${1}" '.[] | select(.name == $n) | .id'
}

# live_ruleset FILE NAME: the live ruleset named NAME.
function live_ruleset() {
  if [ -n "${RULESETS_LIVE_DIR:-}" ]; then
    cat "${RULESETS_LIVE_DIR}/$(basename "${1}")"
    return
  fi
  local id
  id="$(ruleset_id "${2}")"
  [ -n "${id}" ] || { log "❌ no live ruleset named '${2}'"; return 1; }
  api "${API}/repos/$(repo)/rulesets/${id}"
}

# normalized [--no-bypass]: the fields that define a ruleset on stdin, in a stable order.
function normalized() {
  local filter='{name, target, enforcement, conditions, bypass_actors, rules: (.rules | sort_by(.type))}'
  [ "${1:-}" != '--no-bypass' ] || filter="${filter} | del(.bypass_actors)"
  jq -S "${filter}"
}

function check_live() {
  local file name live want have failed=0
  for file in "${RULESETS_DIR}"/*.json; do
    name="$(jq -r .name "${file}")"
    live="$(live_ruleset "${file}" "${name}")" || { failed=1; continue; }
    if jq -e 'has("bypass_actors")' <<< "${live}" > /dev/null; then
      want="$(normalized < "${file}")"
      have="$(normalized <<< "${live}")"
    else
      want="$(normalized --no-bypass < "${file}")"
      have="$(normalized --no-bypass <<< "${live}")"
    fi
    if [ "${want}" = "${have}" ]; then
      log "✅ ${name}: live matches ${file}"
    else
      log "❌ ${name}: live differs from ${file} (make apply-rulesets):"
      log "$(diff <(printf '%s\n' "${want}") <(printf '%s\n' "${have}") || true)"
      failed=1
    fi
  done
  return "${failed}"
}

function apply() {
  if [ -z "${GH_TOKEN:-}" ]; then
    log '❌ GH_TOKEN (admin on the repository) is required'
    exit 1
  fi
  local file name id
  for file in "${RULESETS_DIR}"/*.json; do
    name="$(jq -r .name "${file}")"
    id="$(ruleset_id "${name}")"
    [ -n "${id}" ] || { log "❌ no live ruleset named '${name}'"; exit 1; }
    api -X PUT "${API}/repos/$(repo)/rulesets/${id}" --data-binary "@${file}" > /dev/null
    log "✅ ${name}: applied ${file}"
  done
}

main "${@:-}"
