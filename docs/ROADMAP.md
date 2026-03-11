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


### CI Kubernetes Integration Testing

**What it is:** A CI job that provisions a lightweight Kubernetes cluster (e.g., kind or k3s), installs the Keel Helm chart, and runs the existing Colima integration test suite against it on every pull request.

**Why it matters:** The Colima k8s tests currently run only on developer laptops, which means Kubernetes-level regressions — broken Helm rendering, incorrect RBAC, probe misconfiguration, or incompatible API versions — are not caught until after merge. Running these tests in CI provides three concrete outcomes:

1. **Kubernetes API compatibility:** every PR is verified against the target cluster API version, catching deprecated resource kinds or field names before they reach production.
2. **Helm chart correctness under real conditions:** `helm template` linting catches syntax errors, but only a live install catches runtime failures (missing RBAC permissions, incorrect port names, probe paths that don't match the running binary).
3. **End-to-end signal before merge:** confidence that the binary, chart, and cluster configuration work together, not just individually.

**Implementation approach:** Use `kind` (Kubernetes-in-Docker) in GHA — no external cluster required, no secrets needed, runs on standard `ubuntu-latest` runners. The existing `scripts/colima/` scripts are refactored to accept a kubeconfig path so they work against any cluster, not just Colima.

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

### Full ACME Certificate Manager

**What it is:** Full implementation of the `tls.acme.*` configuration block — wiring `tls.acme.email`, `tls.acme.cache_dir`, and `tls.acme.ca_url` to a live ACME client (Let's Encrypt or any RFC 8555-compatible CA).

**Why it matters:** The `tls.acme.*` fields are parsed today, but the ACME manager is stubbed. The stub logs a warning at startup and falls back to the static cert path. Completing this removes the need to manage TLS certificates manually: Keel would obtain, cache, and automatically renew certificates via the ACME HTTP-01 or TLS-ALPN-01 challenge, with `ca_url` allowing use of private CAs or Let's Encrypt staging.

**Implementation:** Integrate `golang.org/x/crypto/acme/autocert` (or an equivalent library that supports custom CA URLs). `cache_dir` maps to `autocert.DirCache`. Expose port 80 for HTTP-01 challenges when not already in use, or use TLS-ALPN-01 to avoid the extra listener.

**Compliance constraint (non-negotiable):** ACME-obtained certificates and the TLS listener they serve must satisfy all mandatory criteria from `governance/platform.md §32–35`:
- Minimum TLS 1.2; TLS 1.3 required for all new integrations (Keel currently enforces TLS 1.3 minimum via `BuildTLSConfig`).
- Cipher suites: ECDHE key exchange with AES-GCM or ChaCha20-Poly1305 only. RC4, DES, 3DES, export-grade, and sub-2048-bit DHE suites are prohibited.
- Under FIPS builds: key-exchange curves restricted to P-256/P-384 (X25519 excluded); AES-GCM enforced by BoringCrypto.
- HSTS `max-age` ≥ 1 year required on all HTTPS endpoints.

The ACME manager must use `pkg/core/tls.BuildTLSConfig` for the listener `tls.Config` rather than constructing its own, so that the FIPS and non-FIPS policy files remain the single source of truth. The certificate key type requested from the CA must be ECDSA P-256 (or P-384 under FIPS) — RSA 2048 is the `autocert` default and must be overridden.

---

### Replace LICENSE/TRADEMARK Root Symlinks with Real Files

**What it is:** Remove the symlinks at the repository root (`LICENSE → pkg/clisupport/LICENSE`, `TRADEMARK.md → pkg/clisupport/TRADEMARK.md`) and replace them with real files. Add a pre-commit hook script (`scripts/check-legal-drift.sh`) that detects drift between the root copies and the `pkg/clisupport/` copies and tells the developer how to resolve it.

**Why it matters:** The canonical legal text lives in `pkg/clisupport/` because `embed.go` uses `//go:embed` to bake it into the `--check-integrity` command. The root-level symlinks exist so GitHub and tooling see the files at the conventional location. However, symlinks cause several concrete problems:
- **GitHub does not resolve the license badge** — the repository license detection and the "View license" badge in the GitHub UI fail when `LICENSE` is a symlink; GitHub does not follow symlinks when scanning for license files.
- Symlinks are fragile on Windows and break some archive and packaging tools.
- Some editors and IDEs do not resolve symlinks transparently, causing confusing "file not found" behaviour.

Real files at both locations remove all of these issues. The drift-check hook ensures the two copies stay in sync after any edit.

**Drift-check script behaviour (`scripts/check-legal-drift.sh`):**
- If both copies are identical: pass silently.
- If one file is newer (by `git log` date on the file) and they differ: fail and suggest `cp <newer> <older>` to propagate the change.
- If both copies have been modified since their last common commit and they differ: fail and advise the developer to manually merge before committing.

**Implementation:** `scripts/check-legal-drift.sh` called from `.git/hooks/pre-commit`. Checked pairs: `LICENSE` ↔ `pkg/clisupport/LICENSE`, `TRADEMARK.md` ↔ `pkg/clisupport/TRADEMARK.md`.

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
