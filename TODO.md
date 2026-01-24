# TODO.md

## Target Audience
* **Corporate Users**: Infrastructure teams requiring 10+ year stability, FIPS compatibility, and "no-pager" operational excellence.
* **Gen Z "BYOS"**: Independent developers seeking instant-on security, tiny footprints (~2–4 MB), and high-performance "cool factor".

---

## Phase 1: Viral GTM & DX
* [ ] **The "Keel-Haul" CLI**: Standalone companion app to instantly wrap local processes with Keel's security posture via a TUI dashboard.
* [ ] **"Verified Secure" Badge Service**: Tooling to generate a security scorecard/badge based on active build tags to prove hardening.
* [ ] **Build-a-Binary Web UI**: Web configurator for generating specific `go build` commands.
* [ ] **ACME Support**: Integration for automated certificate management (Let's Encrypt).

## Phase 2: Corporate & Compliance
* [ ] **FIPS Monitoring**:
    * **Endpoint**: Implement `/health/fips` returning `{"fips_active": true/false}`.
    * **Metric**: Add `keel_fips_active` gauge (1 or 0) to Prometheus exporter.
* [ ] **FIPS Certification**:
    * **Short-term**: Document "FIPS Compatibility Mode" using BoringSSL/boringcrypto-backed builds.
    * **Long-term**: Pursue formal FIPS 140-2/3 certification for the Keel binary distribution.
* [ ] **OIDC/OAuth2 Proxying**: Support full OIDC redirect flows for providers like Okta or Azure AD.
* [ ] **Immutable Audit Logging**: Dedicated log stream for security events (Authn, 403s, config reloads).
* [ ] **WASM Middleware**: Extension points for WASM-based handlers for multi-language extensibility.

## Phase 3: Infrastructure Excellence
* [ ] **Hot Reload**: Support for zero-downtime configuration updates via `SIGHUP`.
* [ ] **Automatic SBOM**: Embed the Software Bill of Materials directly into the binary.

## TESTING & QUALITY ASSURANCE
* [ ] **BATS-Core Integration**: Create `/tests/suite.bats` to verify binary CLI flags.
* [ ] **Signal Testing**: Use BATS to verify `SIGHUP` config reloads and `SIGTERM` graceful shutdowns.
* [ ] **FIPS Gatekeeper Test**: A BATS test that runs the `max` build in a non-FIPS environment and asserts a non-zero exit code.
* [ ] **Binary Size Check**: BATS script to fail CI if `ci_min.sh` exceeds 3MB.
* [ ] **Integration Testing**: Automated tests for all features using a staging environment.
* [ ] **Performance Testing**: Benchmarking suite to measure throughput and latency under load.
* [ ] **Security Scanning**: Regularly scan the codebase for vulnerabilities using tools like Snyk or Trivy.