# Keel Roadmap

This document tracks planned future capabilities that are not yet implemented. Items are drawn from the project's P18 open backlog. Each item includes context explaining what it is, why it is on the roadmap, and what problem it solves.

This roadmap reflects intent, not commitment. Priorities may shift based on community feedback and real-world usage patterns.

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

### Build-Time Embedded Default Certificate

**What it is:** Support for selecting one TLS certificate at build time and embedding it into the Keel binary.

**Why it matters:** Scratch-style container images have no filesystem. Currently, Keel requires either a cert mounted at runtime (Kubernetes Secret, PVC) or ACME. An embedded default certificate would allow a scratch-image Keel to start serving HTTPS with zero runtime filesystem dependencies — important for air-gapped environments and "does it boot" smoke tests.

**Constraint:** The embedded cert is for development and testing only. The private key would be embedded in the binary (and therefore in the container image layer), making it not a secret. Production deployments must always use runtime-delivered certificates.

**Implementation:** A `go generate` step that encodes a certificate as a Go `[]byte` literal, included conditionally when `build_default_cert` tag is set.

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
