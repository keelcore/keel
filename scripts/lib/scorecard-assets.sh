#!/usr/bin/env bash
# scripts/lib/scorecard-assets.sh
# Release asset names OpenSSF Scorecard (Signed-Releases) detects: provenance as *.intoto.jsonl and
# signatures as *.sigstore.json. Used by scripts/release/build-release-assets.sh. Source this file.

# stage_provenance DIR: each GitHub build attestation's DSSE envelope as <file>.intoto.jsonl, one per
# line. Run before signing, so the provenance is signed and checksummed with the rest.
function stage_provenance() {
  local -r dir="${1}"
  local f out
  for f in "${dir}"/*.github-attestations.json; do
    [ -f "${f}" ] || continue
    out="${f%.github-attestations.json}.intoto.jsonl"
    jq -c '.attestations[]?.bundle.dsseEnvelope // empty' "${f}" > "${out}"
    [ -s "${out}" ] || rm -f "${out}"
  done
}

# stage_sigstore_copies DIR: each cosign bundle also as <file>.sigstore.json. Run after signing.
function stage_sigstore_copies() {
  local -r dir="${1}"
  local b
  for b in "${dir}"/*.bundle; do
    [ -f "${b}" ] || continue
    cp -p "${b}" "${b%.bundle}.sigstore.json"
  done
}
