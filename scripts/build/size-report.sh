#!/usr/bin/env bash
# size-report.sh
# Builds an unstripped debug binary and emits a Pareto table of Go package
# symbol sizes so callers can identify binary bloat.
#
# Usage:
#   scripts/build/size-report.sh                 # default: top 30, max flavor
#   scripts/build/size-report.sh --top 50        # show top 50
#   scripts/build/size-report.sh --flavor min    # analyse the min build

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

readonly LOG_FILE='/tmp/keel_size_report.log'

TOP_N=30
FLAVOR='max'

function main() {
  exec 5>&1
  validate_args "${@:-}"
  parse_flags "${@:-}"

  local debug_bin
  debug_bin="dist/keel-${FLAVOR}-debug"

  prepare_dist
  build_debug "${debug_bin}"
  run_analysis "${debug_bin}"
}

function log() {
  local -r msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a "${LOG_FILE}" >&5
}

function validate_args() {
  local count="${#}"
  if [ "${count}" -eq 1 ] && [ -z "${1:-}" ]; then
    return 0
  fi
  local i=1
  while [ "${i}" -le "${count}" ]; do
    local arg="${!i}"
    case "${arg}" in
      --top)
        local next=$(( i + 1 ))
        if [ "${next}" -gt "${count}" ]; then
          printf 'Error: --top requires a value\n' >&2; exit 1
        fi
        if ! [[ "${!next}" =~ ^[0-9]+$ ]]; then
          printf 'Error: --top requires a positive integer\n' >&2; exit 1
        fi
        i=$(( i + 2 ))
        ;;
      --flavor)
        local next=$(( i + 1 ))
        if [ "${next}" -gt "${count}" ]; then
          printf 'Error: --flavor requires a value\n' >&2; exit 1
        fi
        case "${!next}" in
          min|max|fips) i=$(( i + 2 )) ;;
          *) printf 'Error: --flavor must be min|max|fips\n' >&2; exit 1 ;;
        esac
        ;;
      *)
        printf 'Usage: size-report.sh [--top N] [--flavor min|max|fips]\n' >&2
        exit 1
        ;;
    esac
  done
}

function parse_flags() {
  local count="${#}"
  if [ "${count}" -eq 1 ] && [ -z "${1:-}" ]; then
    return 0
  fi
  local i=1
  while [ "${i}" -le "${count}" ]; do
    local arg="${!i}"
    case "${arg}" in
      --top)    local next=$(( i + 1 )); TOP_N="${!next}";    i=$(( i + 2 )) ;;
      --flavor) local next=$(( i + 1 )); FLAVOR="${!next}";   i=$(( i + 2 )) ;;
      *)        i=$(( i + 1 )) ;;
    esac
  done
}

function tags_for_flavor() {
  case "${1}" in
    min)  printf 'no_fips,no_otel,no_prom,no_remotelog,no_authn,no_h3,no_sidecar' ;;
    max)  printf 'no_fips' ;;
    fips) printf 'fips,no_h3' ;;
  esac
}

function prepare_dist() { mkdir -p 'dist'; }

function build_debug() {
  local -r bin="${1}"
  local tags
  tags="$(tags_for_flavor "${FLAVOR}")"
  log "Building unstripped ${FLAVOR} binary: ${bin}"
  CGO_ENABLED=0 \
    go build -trimpath -tags "${tags}" \
    -o "${bin}" ./cmd/keel
  log "Unstripped size: $(du -sh "${bin}" | cut -f1)"
}

function aggregate_and_print() {
  # mode: 'code' = T/t only; 'all' = everything except U
  local -r mode="${1}" nm_out="${2}" top="${3}"

  local subtotal
  subtotal="$(awk -v mode="${mode}" '
    NF>=4 {
      if (mode=="code" && $3!~/^[Tt]$/) next
      if ($3~/^[Uu]$/) next
      s += $2
    }
    END { printf "%d", s+0 }
  ' "${nm_out}")"

  printf '%-56s  %10s  %6s  %8s\n' 'Package' 'Bytes' '%' 'Cumul%' \
    | tee -a "${LOG_FILE}" >&5
  printf '%-56s  %10s  %6s  %8s\n' '-------' '-----' '-' '------' \
    | tee -a "${LOG_FILE}" >&5

  awk -v mode="${mode}" -v top="${top}" -v subtotal="${subtotal}" '
    NF>=4 {
      if (mode=="code" && $3!~/^[Tt]$/) next
      if ($3~/^[Uu]$/) next
      size = $2 + 0
      name = $4
      if (name ~ /^go:/ || name == "") {
        name = "(go-internal)"
      } else {
        sub(/\(.*/, "", name)
        sub(/\.[^.]*$/, "", name)
      }
      pkg[name] += size
    }
    END {
      for (p in pkg) printf "%d %s\n", pkg[p], p
    }
  ' "${nm_out}" \
    | sort -rn \
    | awk -v top="${top}" -v subtotal="${subtotal}" '
      BEGIN { cumul = 0; n = 0 }
      {
        if (++n > top) next
        size = $1; pkg = $2
        pct   = (subtotal > 0) ? (size / subtotal * 100) : 0
        cumul += pct
        printf "%-56s  %10d  %5.1f%%  %7.1f%%\n", pkg, size, pct, cumul
      }
    ' | tee -a "${LOG_FILE}" >&5

  local sub_mb
  sub_mb="$(awk "BEGIN{printf \"%.2f\", ${subtotal}/1048576}")"
  log "  Subtotal: ${subtotal} bytes (${sub_mb} MB)"
}

function run_analysis() {
  local -r bin="${1}"
  local nm_out
  nm_out="$(mktemp /tmp/keel_nm_XXXXXX)"
  # shellcheck disable=SC2064
  trap "rm -f '${nm_out}'" RETURN

  go tool nm -size "${bin}" > "${nm_out}"

  log ""
  log "Symbol-size Pareto — ${FLAVOR} flavor (top ${TOP_N} packages)"
  log "Binary: ${bin}"
  log "$(printf '%0.s─' {1..80})"
  printf '%-56s  %10s  %6s  %8s\n' 'Package' 'Bytes' '%' 'Cumul%' \
    | tee -a "${LOG_FILE}" >&5
  printf '%-56s  %10s  %6s  %8s\n' '-------' '-----' '-' '------' \
    | tee -a "${LOG_FILE}" >&5

  # Pass 2a: code only (T/t) — what the linker tree-shook to keep.
  log ""
  log "  CODE symbols only (T/t) — what's actually executing:"
  log ""
  aggregate_and_print 'code' "${nm_out}" "${TOP_N}"

  # Pass 2b: all symbol types — full size picture including data/rodata.
  log ""
  log "  ALL symbol types — full size picture (incl. rodata, data):"
  log ""
  aggregate_and_print 'all' "${nm_out}" "${TOP_N}"

  log ""
  log "Note: rodata blobs (e.g. Go 1.25 embedded FIPS140 module) inflate the 'all' total."
  log "      Stripped release binary is smaller than these symbol totals suggest."
}

main "${@:-}"
