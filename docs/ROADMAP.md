# Keel Roadmap

This document tracks planned future capabilities that are not yet implemented. Items are drawn from the project's P18 open backlog. Each item includes context explaining what it is, why it is on the roadmap, and what problem it solves.

This roadmap reflects intent, not commitment. Priorities may shift based on community feedback and real-world usage patterns.

> **Maintainer note:** Each entry below is mirrored as a GitHub issue labelled `roadmap`.
> To sync new entries after editing this file, run:
> ```sh
> scripts/roadmap-issues.sh
> ```
> The script is idempotent — it skips entries that already have a matching issue (open or closed).

---

## Planned Items

### "Keel-Haul" CLI — Standalone TUI Companion

**What it is:** A terminal UI (TUI) companion tool that wraps local processes with Keel's security posture, similar to how you would run a process under a supervisor.

**Why it matters:** Currently, "keel-hauling" a legacy service (forcing it into modern security compliance without rewriting it) requires configuring Keel in sidecar mode and running both processes, typically via Docker Compose or a process manager. The Keel-Haul CLI would make this a one-command operation: `keel-haul ./my-legacy-binary --port 3000`. It would start the upstream process, start Keel in sidecar mode pointed at it, and provide a live TUI showing traffic, health, and metrics.

**Target users:** Developers who want to locally test what their service looks like behind Keel's security layer, and operators who want to quickly wrap a legacy process on a VM without setting up Kubernetes.

---

### "Verified Secure" Badge Service

**What it is:** A service that generates a scorecard and embeddable badge showing which Keel build tags are active in a given binary, and what security posture that implies.

**Why it matters:** A repository badge like "[![Keel: Full Security](https://badge.keel.dev/my-binary)](https://badge.keel.dev/my-binary)" provides instant, verifiable signal that a project is running with OWASP hardening, FIPS crypto, authn, and no disabled security features. The badge links to a scorecard page with details.

**Technical approach:** The binary embeds its build tags (this is already supported via the `/version` admin endpoint). The badge service queries a deployed instance's `/version` endpoint and generates the scorecard. For offline use, the badge can be generated from a binary artifact at build time.

---

### Build-a-Binary Web UI

**What it is:** A web configurator for generating `go build` commands with the correct build tags for your use case.

**Why it matters:** Keel's build tag system is powerful but requires knowing all the available tags and their implications. A web UI would let you check boxes ("I need FIPS," "I don't need HTTP/3," "I don't need StatsD"), see the resulting binary size estimate, and get a copy-paste `go build` command.

**Example output:**
```sh
# For: FIPS + minimal observability, no H3, no StatsD
GOEXPERIMENT=boringcrypto go build \
  -tags 'no_h3,no_statsd,no_remotelog' \
  ./cmd/keel
# Estimated binary size: ~4–5 MB
```

---

### OIDC / OAuth2 Proxying

**What it is:** Full OIDC redirect flow support — Okta, Azure AD, Google, Keycloak — so Keel can act as an authentication proxy for services that do not implement authn themselves.

**Why it matters:** Currently, Keel's authn layer validates JWT bearer tokens. This requires the client to already have a JWT — it does not handle the case where a human user needs to log in via a browser-based OIDC flow. OIDC proxying would allow Keel to:
1. Detect unauthenticated browser requests.
2. Redirect to the OIDC provider's authorization endpoint.
3. Handle the callback, exchange the code for tokens.
4. Set a session cookie.
5. Forward subsequent requests with the validated identity.

This fills the gap between Keel's current service-to-service authn (JWT) and user-facing web application authn (OIDC).

---

### Immutable Audit Logging

**What it is:** A dedicated log stream for security events (authentication successes/failures, 403 responses, config reloads, TLS errors) written to an append-only, tamper-evident log.

**Why it matters:** Access logs capture all requests. Audit logs capture security-relevant events at a higher signal-to-noise ratio, formatted for ingestion by SIEM systems (Splunk, QRadar, Microsoft Sentinel). The "immutable" property (append-only, optionally with cryptographic chaining) provides forensic integrity — an attacker who compromises the system cannot easily erase evidence of the intrusion from the audit log.

**Implementation:** A separate log writer (not the access log) that emits to a dedicated file, syslog/journald, or remote endpoint, with each entry containing: event type, principal ID, source IP, outcome, timestamp, and an optional chain hash linking to the previous entry.

---

### WASM Middleware Extension Points

**What it is:** Extension points that allow loading WebAssembly (WASM) modules as middleware handlers.

**Why it matters:** Keel's build-time opt-out model is powerful but requires recompiling to change behavior. WASM middleware would allow extending Keel's request pipeline at runtime, without recompiling. A WASM module could implement custom authn logic, request transformation, rate limiting, or content inspection.

**Prior art:** Envoy's WASM filter mechanism, Traefik's WASM middleware support.

**Constraint:** WASM middleware execution has measurable overhead. It is appropriate for low-traffic, high-complexity use cases, not high-throughput data paths.

---


### TRADEMARK.md Periodic Review

**What it is:** A scheduled periodic review (annually) of TRADEMARK.md to ensure the trademark policy is current and reflects actual usage in the ecosystem.

**Why it matters:** As the project grows and downstream users build on Keel, the trademark policy needs to evolve. What constitutes "permitted use" and "restricted use" should reflect real-world patterns, not assumptions made at project inception.

---

### OTLP Tracing Implementation

**What it is:** Full implementation of the `tracing.otlp.*` configuration block — wiring `tracing.otlp.enabled`, `tracing.otlp.endpoint`, and `tracing.otlp.insecure` to a real OpenTelemetry OTLP exporter.

**Why it matters:** The `tracing.otlp.*` config fields are parsed and validated today, but the tracing pipeline is not started. Completing this wires the fields to the `go.opentelemetry.io/otel/exporters/otlp/otlptrace` exporter so that every proxied request produces a span in the operator's tracing backend (Jaeger, Tempo, Honeycomb, etc.). This closes the observability gap between metrics (Prometheus, StatsD) and distributed tracing.

**Implementation:** Create a `tracing` package (or extend the existing stub) that initialises an OTLP gRPC/HTTP exporter from the config, registers it as the global tracer provider, and injects `otelhttp.NewHandler` into the middleware stack. Honour `tracing.otlp.insecure` for dev environments that do not terminate TLS at the collector.

---

### Comprehensive TLS Cipher and Curve Hardening

**What it is:** A full audit and hardening pass ensuring every outbound TLS connection Keel makes — not just its server-side listener — applies the same cipher-suite and key-exchange policy enforced by `pkg/core/tls.BuildTLSConfig`.

**Why it matters:** The current `BuildTLSConfig` policy (`policy_fips.go` / `policy.go`) is applied to the server-side TLS listener. Outbound connections — ACME CA endpoint (`httpClientWithCA`), remote log sink, ext-authz transport, and future OTLP exporter — each construct their own `tls.Config{}` and do not go through that policy. This means:

1. Under a FIPS toolchain (`GOFIPS140=only` / BoringCrypto), all crypto is enforced at the runtime level regardless of `tls.Config` content — so the gap is closed implicitly but not explicitly.
2. Without the FIPS toolchain (e.g. the `fips` build tag used as a policy signal), outbound connections may negotiate X25519 key exchange or ChaCha20-Poly1305, which are not approved under FIPS 140-2/3.
3. As new transports are added (OTLP, ext-authz gRPC, future federation), the policy must not be re-invented per-call-site.

**What "comprehensive" means:**

- Audit every `tls.Config{...}` construction in the codebase; replace raw construction with a call to (or extension of) `BuildTLSConfig`.
- For outbound clients: derive a `*tls.Config` from `BuildTLSConfig`, then layer caller-specific fields on top (e.g. `RootCAs` for private CAs, `ServerName` for SNI overrides).
- Enumerate cipher suites **explicitly** in `policy_fips.go` rather than relying solely on BoringCrypto runtime enforcement — defence-in-depth; catches misconfiguration before it reaches the crypto layer.
- Add a unit test that constructs every outbound HTTP/gRPC client in the codebase and asserts `MinVersion >= TLS 1.2`, no RC4/DES/3DES in `CipherSuites`, and — under the `fips` tag — no X25519 in `CurvePreferences`.

**Concrete gap as of 2026-03-12:**

| Call site | File | Gap |
|---|---|---|
| `httpClientWithCA` | `pkg/core/acme/manager.go` | Raw `&tls.Config{RootCAs: pool}` — no MinVersion, no curve policy |
| Remote log HTTP sink | `pkg/core/server.go` | Uses `http.DefaultTransport` — no TLS policy |
| ext-authz HTTP transport | `pkg/core/mw/ext_authz.go` | Default `http.Client` — no TLS policy |
| OTLP exporter (future) | not yet implemented | Must apply policy at introduction time |

**Acceptance criteria:**

- `go vet ./...` and `staticcheck ./...` continue to pass.
- All call sites above go through `BuildTLSConfig` (or a validated wrapper).
- `policy_fips.go` enumerates `CipherSuites` explicitly.
- New unit test `TestOutboundTLSPolicy` passes under both `fips` and non-`fips` build tags.
- BATS: integrity test confirms ACME client successfully connects to pebble with FIPS-policy TLS settings.

---

### Keel-Wrapped Helm Charts Architecture

**What it is:** A library of drop-in secure Helm chart replacements that wrap existing upstream charts with a Keel sidecar container — without modifying upstream chart templates. Each wrapper chart ships as an OCI artifact at `ghcr.io/keel/helm/keel-<name>`.

**Why it matters:** Operators running commodity workloads (nginx, Grafana, MinIO, Keycloak, etc.) currently have no path to adding TLS termination, JWT authentication, OWASP headers, request protection, and observability without forking upstream charts or adding a separate ingress layer. A wrapper chart is a strict-dependency overlay: the upstream chart is vendored unchanged as a Helm dependency; the wrapper contributes only a second Deployment (Keel + upstream container in the same pod) and a Service that exposes Keel exclusively.

**Design invariants:**
1. The upstream chart is a declared `dependencies:` entry. Its templates are never modified.
2. Upstream runtime behaviour is controlled only via `values.yaml` overrides passed through to the dependency.
3. Wrapper `values.yaml` is a strict superset of upstream values: every upstream field is preserved under its upstream key; Keel-specific fields live under a top-level `keel:` namespace.
4. Wrapper chart `version` matches the upstream dependency `version` exactly (same semver).
5. Traffic flows: client → Keel container (external port) → `localhost` → upstream container (internal port). The upstream container is never exposed by the Service.
6. The Keel container image digest must be attested as a cosign OCI signature on every chart release, matching the attestation produced by the main Keel binary release pipeline.

**Pod structure:**
```
Pod
 ├─ keel      (listens on external port, e.g. 8443)
 └─ upstream  (binds 127.0.0.1:<internal port>)
```

**OCI registry layout:** `ghcr.io/keel/helm/keel-<name>` — installed as `helm install <name> oci://ghcr.io/keel/helm/keel-<name>` instead of the upstream `helm install <name> <repo>/<name>`.

**Initial target charts (tracked individually as child issues):** nginx, grafana, prometheus, minio, argo-cd, loki, tempo, elasticsearch, kibana, keycloak.
