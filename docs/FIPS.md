# Keel FIPS Compliance Guide

This document explains FIPS mode in Keel: what FIPS is, why it matters, how to build and run a FIPS-compliant Keel
binary, how to verify FIPS mode at runtime, known constraints, and the current certification status.

---

## 1. What FIPS Is and Why It Matters

**FIPS 140** (Federal Information Processing Standard 140) is a US government standard that defines requirements for
cryptographic modules used in federal information systems. It is published by NIST (National Institute of Standards and
Technology) and has two active versions: FIPS 140-2 and the newer FIPS 140-3.

FIPS validation means that a specific cryptographic module implementation (a library) has been tested by an accredited
laboratory and certified by NIST to meet the standard's requirements. The key word is "validated" — using a FIPS-approved
algorithm (like AES-256) is not enough. The specific implementation (the code that runs) must have been through the
validation process.

**Who requires FIPS:**

- US federal agencies and their contractors (required by law via FISMA).
- FedRAMP-authorized cloud service providers.
- Healthcare organizations under HIPAA security guidelines.
- Financial institutions under PCI DSS in some configurations.
- Defense contractors under CMMC Level 3.
- Any organization that processes US government classified or controlled unclassified information.

**What FIPS restricts:**

- Only FIPS-approved cryptographic algorithms may be used.
- Only FIPS-validated implementations of those algorithms may be used.
- Algorithms that are not approved (MD5, SHA1 for digital signatures, DES, RC4) cannot be used, even for non-security
  purposes like hash tables or checksums in non-sensitive code paths.

**The practical impact for a Go HTTP server:**

- TLS must use only FIPS-approved cipher suites.
- Certificate signatures must use FIPS-approved algorithms.
- JWT signing/verification must use FIPS-approved algorithms.
- HMAC, key derivation, and random number generation must use FIPS-approved implementations.

---

## 2. BoringCrypto and BoringSSL

**The problem with standard Go crypto:** Go's standard `crypto/*` packages are not FIPS-validated. They implement
FIPS-approved algorithms (AES, SHA-256, ECDSA) but the implementations themselves have not been through the FIPS
validation process. You cannot use them in a FIPS-required environment.

**Google's solution:** Google maintains **BoringSSL**, a fork of OpenSSL with FIPS-validated cryptographic module
implementations. For Go, Google provides **BoringCrypto** — a mechanism that replaces Go's standard crypto
implementations with calls into BoringSSL's FIPS-validated module.

When compiled with BoringCrypto, Go's `crypto/tls`, `crypto/rsa`, `crypto/ecdsa`, `crypto/sha256`, etc. are redirected
to BoringSSL's implementations. The FIPS module boundary is at BoringSSL — it is the FIPS-validated component, not the
Go wrapper.

**The `boringcrypto` experiment:** BoringCrypto was available as a `GOEXPERIMENT=boringcrypto` build experiment in Go
1.17–1.23. Starting in Go 1.24, it is activated via `GOEXPERIMENT=boringcrypto` or via the `GOFIPS140` environment
variable mechanism.

**The `GOFIPS140` environment variable:** In Go 1.24+, the standard library includes a built-in FIPS 140 module
(`crypto/internal/fips140`). Setting `GOFIPS140=latest` at runtime activates FIPS 140-3 mode using this built-in
module, without requiring a special build. Setting `GOFIPS140=<version>` activates a specific certified snapshot. See
the [Go FIPS documentation](https://go.dev/doc/security/fips140) for details.

---

## 3. How to Build a FIPS-Compliant Keel Binary

**Build-time opt-out:** `no_fips`

### 3.1 Using the `fips` Image Flavor (Helm / Docker)

The simplest approach: use the pre-built `fips` image flavor in the Helm chart.

```yaml
# values.yaml
image:
  flavor: fips
```

The `fips` image is built with `GOEXPERIMENT=boringcrypto` and the `GOFIPS140` environment variable set. It
uses the BoringCrypto-backed crypto. No other changes are needed.

### 3.2 Building Your Own FIPS Binary

```sh
# Go 1.24+ — built-in FIPS 140 module
GOFIPS140=latest go build ./cmd/keel

# Or with BoringCrypto experiment explicitly (Go 1.17–1.23 style, still works in 1.24+)
GOEXPERIMENT=boringcrypto go build ./cmd/keel

# Verify the binary uses BoringCrypto
go tool nm keel | grep -i boring
# Should output symbols like: runtime/internal/sys.boringcrypto or similar
```

### 3.3 Verifying the Binary at Build Time

```sh
# Check that the binary was compiled with boringcrypto
go version -m ./keel | grep GOEXPERIMENT
# Should output: GOEXPERIMENT=boringcrypto (or similar)

# For Go 1.24+ GOFIPS140 builds:
go version -m ./keel | grep GOFIPS140
```

### 3.4 Running with FIPS Mode Active

```sh
# Go 1.24+: activate FIPS 140-3 at runtime (if binary supports it)
GOFIPS140=latest ./keel --config keel.yaml

# Or activate FIPS mode via GODEBUG
GODEBUG=fips140=only ./keel --config keel.yaml
```

**`GODEBUG=fips140=only`** is the strictest setting — it causes the Go runtime to panic if any non-FIPS code path in the
standard library is executed. Use this during testing to surface hidden non-FIPS crypto usage.

---

## 4. Verifying FIPS Mode at Runtime

### 4.1 Admin Endpoint

```sh
# Check if FIPS mode is active
curl http://localhost:9999/health/fips
# Response: {"fips_active": true}
# Or:       {"fips_active": false}
```

**What `fips_active: true` means:** The Keel binary was compiled with BoringCrypto and FIPS 140 mode is active. All
cryptographic operations in TLS, JWT signing/verification, and any other crypto in Keel are going through BoringSSL's
FIPS-validated module.

**What `fips_active: false` means:** Either the binary was not compiled with BoringCrypto, or FIPS mode was not activated
at runtime. Not suitable for FIPS-required environments.

### 4.2 Prometheus Metric

```promql
# Is FIPS mode active?
keel_fips_active == 1
```

Alert if `keel_fips_active == 0` in environments that require FIPS:

```yaml
- alert: KeelFIPSNotActive
  expr: keel_fips_active == 0
  for: 0m
  labels:
    severity: critical
  annotations:
    summary: "Keel is running without FIPS mode in a FIPS-required environment"
```

### 4.3 Version Endpoint

```sh
curl http://localhost:9999/version | jq .fips_active
```

---

## 5. FIPS Constraints and Known Limitations

When FIPS mode is active, the following constraints apply:

### 5.1 TLS

- **TLS 1.3 only.** TLS 1.2 is not supported by Keel regardless of FIPS mode (Keel is TLS 1.3-only by design). In FIPS
  mode, this is also a hard requirement.
- **Approved cipher suites only.** TLS 1.3 cipher suites are fixed by the protocol; in FIPS mode they must be from the
  FIPS-approved list:
  - `TLS_AES_128_GCM_SHA256`
  - `TLS_AES_256_GCM_SHA384`
- **TLS_CHACHA20_POLY1305_SHA256 is not approved** for FIPS. In FIPS mode, this suite is excluded from negotiation.

### 5.2 JWT

- **HS256 (HMAC-SHA256):** Approved. Shared-secret JWTs work in FIPS mode.
- **RS256 (RSA-SHA256):** Approved. RSA key sizes must be >= 2048 bits. 4096 bits recommended.
- **ES256 (ECDSA-P256-SHA256):** Approved. P-256 (secp256r1) is a FIPS-approved curve.
- **ES384 (ECDSA-P384-SHA384):** Approved.
- **HS512, RS512, ES512:** Approved (SHA-512 is FIPS-approved).
- **EdDSA (Ed25519):** **Not FIPS-approved.** Cannot be used in FIPS mode.

### 5.3 Certificates

- Certificate signatures must use FIPS-approved algorithms (SHA-256, SHA-384 — not SHA-1 or MD5).
- RSA keys must be >= 2048 bits. EC keys must use P-256, P-384, or P-521.
- Ed25519 certificates are not accepted in FIPS mode.

### 5.4 ACME

ACME certificate management works in FIPS mode, subject to the certificate constraints above. Let's Encrypt issues
certificates with FIPS-compliant signatures (SHA-256, RSA-2048+, or ECDSA P-256/P-384).

### 5.5 Test Code

Integration tests and unit tests that use `httptest.NewTLSServer` or `tls.Config{InsecureSkipVerify: true}` cannot run
in FIPS mode because `httptest` uses self-signed certificates with algorithms that may not be FIPS-approved. Tests that
hit these code paths must guard:

```go
func TestSomething(t *testing.T) {
    if os.Getenv("GOFIPS140") != "" || strings.Contains(os.Getenv("GODEBUG"), "fips140=only") {
        t.Skip("skipping in FIPS mode: uses self-signed test cert")
    }
    // ... test code
}
```

---

## 6. Build Tag: `fips` Flavor vs. `no_*` Tags

The `fips` image flavor is not a single build tag — it is a combination of build configuration choices:

- Compiled with `GOEXPERIMENT=boringcrypto` (or `GOFIPS140=latest` in Go 1.24+).
- `no_h3` is implied (HTTP/3's QUIC implementation uses crypto that may not be FIPS-approved).
- All other features are available.

To check whether the running binary was built for FIPS:

```sh
go version -m /usr/bin/keel | grep -E "GOEXPERIMENT|GOFIPS140"
```

---

## 7. Certification Status

**Current status:** Keel is **FIPS-compatible but not independently FIPS-certified.**

What this means:

- When built with BoringCrypto, Keel uses cryptographic operations provided by Google's BoringSSL, which **is** FIPS
  140-2 validated (NIST certificate #3678 for BoringCrypto as of Go 1.21 builds; check the current
  [NIST CMVP database](https://csrc.nist.gov/projects/cryptographic-module-validation-program) for the latest status).
- Keel itself, as a binary, has not been submitted for independent FIPS 140 validation. Formal certification of the Keel
  binary distribution is on the roadmap.

**For compliance purposes:** Most FIPS-required environments (FedRAMP, FISMA) require use of FIPS-validated cryptographic
modules, not necessarily that the entire application be certified. Running the Keel `fips` flavor uses BoringSSL's
validated module for all cryptographic operations, which is typically sufficient. Consult your compliance officer or
accreditor for your specific requirements.

**Certification roadmap:** Formal FIPS 140-3 certification of the Keel binary is planned. Track progress in
[docs/ROADMAP.md](ROADMAP.md).

---

## 8. Testing FIPS Compliance

To verify your Keel deployment is operating in FIPS mode end-to-end:

```sh
# 1. Verify the binary
go version -m $(which keel) | grep -E "GOEXPERIMENT|GOFIPS140"

# 2. Verify at runtime via health endpoint
curl -s http://localhost:9999/health/fips | jq .fips_active

# 3. Verify via Prometheus metric
curl -s http://localhost:9999/metrics | grep keel_fips_active

# 4. Verify TLS negotiated TLS 1.3
openssl s_client -connect localhost:8443 -tls1_3 < /dev/null 2>&1 | grep "Protocol"
# Should output: Protocol  : TLSv1.3

# 5. Verify TLS 1.2 is rejected
openssl s_client -connect localhost:8443 -tls1_2 < /dev/null 2>&1 | grep -E "alert|handshake failure"
# Should output a handshake failure (Keel rejects TLS 1.2)

# 6. Run FIPS compliance test suite (when available)
GOFIPS140=latest go test -tags fips ./tests/fips/...
```
