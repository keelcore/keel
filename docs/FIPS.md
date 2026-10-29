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

## 2. The Go Cryptographic Module

Since Go 1.24 the standard library contains a FIPS 140-3 cryptographic module, the **Go Cryptographic Module**
(`crypto/internal/fips140/...`). Every `crypto/*` package — `crypto/tls`, `crypto/ecdsa`, `crypto/rsa`,
`crypto/sha256`, `crypto/hmac` — is implemented on top of it. It is pure Go: no cgo, no external library, so FIPS
binaries stay static and cross-compile like any other Go build.

**Module versions.** The Go team freezes a module version at each CMVP validation (Google contracts Geomys for at least
yearly validations) and ships the frozen copies with the toolchain:

| Version | Frozen from | CMVP status |
| --- | --- | --- |
| `v1.0.0` | Go 1.24 | Certificate #5247 (FIPS 140-3), CAVP A6650 |
| `v1.26.0` | Go 1.26 | In review (CAVP A8028); no certificate yet |

Once a newer version is certified, the Go toolchain drops the older frozen copies. Check the
[NIST CMVP database](https://csrc.nist.gov/projects/cryptographic-module-validation-program) and
[go.dev/doc/security/fips140](https://go.dev/doc/security/fips140) for the current state before citing either to an
auditor.

**Build-time selection (`GOFIPS140`).** `GOFIPS140` picks which module is compiled in and turns FIPS mode on by default
(`DefaultGODEBUG=fips140=on`):

- `off` (default) — the module from the current toolchain, FIPS mode off.
- `latest` — the module from the current toolchain, FIPS mode on. **Not a certified module.**
- `v1.0.0`, `v1.26.0` — a specific frozen module, FIPS mode on.
- `certified` / `inprocess` — aliases for the newest certified / in-review version.

Keel pins `GOFIPS140=v1.0.0` (`scripts/build/ci_max.sh`), so a FIPS build always names exactly the certified module.
Moving to a newer module is a deliberate one-line change; if the toolchain drops `v1.0.0`, the build fails rather than
silently switching modules.

**Run-time setting (`GODEBUG`).** `GODEBUG=fips140=on` enables FIPS mode; `GODEBUG=fips140=only` additionally makes
non-approved algorithms fail. Go documents `only` as a testing/debugging aid. Keel's FIPS binary deliberately requires
it (see §3.3).

---

## 3. How to Build a FIPS-Compliant Keel Binary

**Build-time opt-out:** `no_fips`

### 3.1 The `fips` Image Flavor (Docker / Helm)

The `fips` image is built from `scripts/deploy/docker/Dockerfile` with `FLAVOR=fips`, which runs `make max`. Deploy that
image tag with the Helm chart; no other configuration is needed.

### 3.2 Building Your Own FIPS Binary

```sh
make max            # dist/keel-fips: GOFIPS140=v1.0.0, tags fips,no_h3
```

`make max` also runs the test suite with `GODEBUG=fips140=only` to prove no code path needs non-approved crypto.
Equivalent manual build:

```sh
GOFIPS140=v1.0.0 CGO_ENABLED=0 go build -trimpath -tags 'fips,no_h3' -o keel-fips ./cmd/keel
```

### 3.3 Running the FIPS Binary

```sh
GODEBUG=fips140=only ./keel-fips
```

The FIPS binary **fails closed**: unless `GODEBUG` contains `fips140=only` it exits immediately with
`Failing closed: FIPS build requires GODEBUG=fips140=only`. This guarantees the process can never run with
non-approved cryptography, at the cost of requiring the strict mode explicitly.

### 3.4 Verifying the Binary at Build Time

```sh
go version -m ./keel-fips | grep -E 'GOFIPS140|fips140'
# build   GOFIPS140=v1.0.0-c2097c7c
# build   DefaultGODEBUG=fips140=on
```

`make release-ci` with `FIPS_MODE=true` fails the release unless `dist/keel-fips` records `GOFIPS140=v1.0.0`.

---

## 4. Verifying FIPS Mode at Runtime

All three signals below report the same value: true only for a FIPS build (`!no_fips`) whose Go Cryptographic Module
reports FIPS 140 mode (`crypto/fips140.Enabled()`). Environment variables cannot fake it.

### 4.1 Admin Endpoint

```sh
curl http://localhost:9999/health/fips
# {"fips_active": true}
```

**`fips_active: true`:** this is a FIPS build and every cryptographic operation in Keel — TLS, JWT signing and
verification, HMAC, random numbers — runs inside the Go Cryptographic Module in FIPS 140 mode.

**`fips_active: false`:** either the binary was built with `no_fips`, or FIPS mode is off. Not suitable for
FIPS-required environments.

### 4.2 Prometheus Metric

```promql
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

With `fips.monitor: true`, Keel also re-checks hourly and increments `keel_fips_monitor_failures_total` on failure.

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

## 6. Build Tags of the `fips` Flavor

The `fips` flavor is a build configuration, not a single tag:

- `GOFIPS140=v1.0.0` — compiles in the certified Go Cryptographic Module and enables FIPS mode by default.
- Tag `fips` — FIPS TLS policy (P-256/P-384 only), the fail-closed `GODEBUG=fips140=only` gate, FIPS status reporting.
- Tag `no_h3` — HTTP/3 is compiled out; QUIC is not compatible with `fips140=only`.
- All other features are available.

---

## 7. Certification Status

**Current status:** Keel is **FIPS-compatible but not independently FIPS-certified.**

- The `fips` flavor performs all cryptography in the Go Cryptographic Module `v1.0.0`, which holds CMVP certificate
  #5247 (FIPS 140-3). Confirm the current status in the
  [NIST CMVP database](https://csrc.nist.gov/projects/cryptographic-module-validation-program).
- Keel itself, as a binary, has not been submitted for independent FIPS 140 validation.

**For compliance purposes:** most FIPS-required environments (FedRAMP, FISMA) require FIPS-validated cryptographic
modules, not certification of the whole application. Running the Keel `fips` flavor uses a validated module for all
cryptographic operations, which is typically sufficient. Consult your compliance officer or accreditor for your
specific requirements. The Go team states it cannot guarantee that using the module satisfies any particular
regulation.

**Certification roadmap:** formal FIPS 140-3 certification of the Keel binary is planned. Track progress in
[docs/ROADMAP.md](ROADMAP.md).

---

## 8. Testing FIPS Compliance

To verify a Keel deployment is operating in FIPS mode end to end:

```sh
# 1. Verify the binary records the certified module
go version -m ./keel-fips | grep GOFIPS140          # GOFIPS140=v1.0.0-...

# 2. Verify at runtime via the admin endpoint
curl -s http://localhost:9999/health/fips | jq .fips_active

# 3. Verify via Prometheus metric
curl -s http://localhost:9999/metrics | grep keel_fips_active

# 4. Verify TLS negotiated TLS 1.3
openssl s_client -connect localhost:8443 -tls1_3 < /dev/null 2>&1 | grep "Protocol"
# Should output: Protocol  : TLSv1.3

# 5. Verify TLS 1.2 is rejected
openssl s_client -connect localhost:8443 -tls1_2 < /dev/null 2>&1 | grep -E "alert|handshake failure"

# 6. Re-run the suite under strict FIPS mode
make max
```
