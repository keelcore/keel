#!/usr/bin/env bash
# gen-certs.sh
# Generates self-signed TLS certificates for integration testing.
# Produces CA, server (with SANs), and client cert/key pairs under tests/fixtures/certs/.
# Output directory is gitignored; run once before TLS-dependent tests.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

function main() {
  exec 5>&1
  validate_args "${@:-}"
  local script_dir
  script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  generate_all "${script_dir}/certs"
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_gen_certs.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument(s)'
    exit 1
  fi
}

function generate_all() {
  local -r cert_dir="${1}"
  log "🔑 Generating TLS test certificates in ${cert_dir}..."
  ensure_dir "${cert_dir}"
  generate_ca "${cert_dir}"
  generate_server_cert "${cert_dir}"
  generate_client_cert "${cert_dir}"
  cleanup_temp "${cert_dir}"
  log '✅ Certificates generated successfully'
  list_certs "${cert_dir}"
}

function ensure_dir() {
  local -r dir="${1}"
  log "📁 Ensuring certificate directory: ${dir}"
  mkdir -p "${dir}"
}

function generate_ca() {
  local -r dir="${1}"
  log 'Generating CA key and self-signed certificate...'
  openssl genrsa -out "${dir}/ca.key" 4096 2>/dev/null
  openssl req -new -x509 -days 3650 \
    -key "${dir}/ca.key" \
    -out "${dir}/ca.crt" \
    -subj '/CN=Keel Test CA/O=Keel Test/C=US' 2>/dev/null
  log '✅ CA generated'
}

function write_server_ext() {
  local -r path="${1}"
  printf '%s\n' '[v3_req]'                                          > "${path}"
  printf '%s\n' 'subjectAltName = @alt_names'                      >> "${path}"
  printf '%s\n' 'keyUsage = critical, digitalSignature, keyEncipherment' >> "${path}"
  printf '%s\n' 'extendedKeyUsage = serverAuth'                    >> "${path}"
  printf '%s\n' ''                                                  >> "${path}"
  printf '%s\n' '[alt_names]'                                       >> "${path}"
  printf '%s\n' 'DNS.1 = localhost'                                 >> "${path}"
  printf '%s\n' 'DNS.2 = keel'                                      >> "${path}"
  printf '%s\n' 'IP.1  = 127.0.0.1'                                >> "${path}"
}

function generate_server_cert() {
  local -r dir="${1}"
  log 'Generating server key, CSR, and signed certificate...'
  openssl genrsa -out "${dir}/server.key" 2048 2>/dev/null
  openssl req -new -key "${dir}/server.key" -out "${dir}/server.csr" \
    -subj '/CN=localhost/O=Keel Test/C=US' 2>/dev/null
  write_server_ext "${dir}/server-ext.cnf"
  openssl x509 -req -days 365 \
    -in "${dir}/server.csr" -CA "${dir}/ca.crt" -CAkey "${dir}/ca.key" \
    -CAcreateserial -out "${dir}/server.crt" \
    -extensions v3_req -extfile "${dir}/server-ext.cnf" 2>/dev/null
  log '✅ Server certificate generated'
}

function generate_client_cert() {
  local -r dir="${1}"
  log 'Generating client key and mTLS certificate...'
  openssl genrsa -out "${dir}/client.key" 2048 2>/dev/null
  openssl req -new -key "${dir}/client.key" -out "${dir}/client.csr" \
    -subj '/CN=test-client/O=Keel Test/C=US' 2>/dev/null
  openssl x509 -req -days 365 \
    -in "${dir}/client.csr" -CA "${dir}/ca.crt" -CAkey "${dir}/ca.key" \
    -CAcreateserial -out "${dir}/client.crt" 2>/dev/null
  log '✅ Client certificate generated'
}

function cleanup_temp() {
  local -r dir="${1}"
  log 'Removing temporary CSR and extension files...'
  rm -f "${dir}/server.csr" "${dir}/client.csr" "${dir}/server-ext.cnf"
  # Keys are test fixtures mounted into containers running as a different UID.
  # chmod 644 ensures the container user can read them on Linux bind mounts.
  chmod 644 "${dir}/server.key" "${dir}/client.key" "${dir}/ca.key"
}

function list_certs() {
  local -r dir="${1}"
  log '📄 Generated files:'
  ls -lh "${dir}" | tee -a '/tmp/keel_gen_certs.log' >&5
}

main "${@:-}"
