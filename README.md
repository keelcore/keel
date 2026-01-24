# HTTP(S) Server Kit (Golang)

[![CI](https://img.shields.io/github/actions/workflow/status/OWNER/REPO/ci.yml?branch=main)](https://github.com/OWNER/REPO/actions)
[![Release](https://img.shields.io/github/v/release/OWNER/REPO)](https://github.com/OWNER/REPO/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/OWNER/REPO.svg)](https://pkg.go.dev/github.com/OWNER/REPO)
[![Go Report Card](https://goreportcard.com/badge/github.com/OWNER/REPO)](https://goreportcard.com/report/github.com/OWNER/REPO)
[![Coverage](https://img.shields.io/codecov/c/github/OWNER/REPO)](https://codecov.io/gh/OWNER/REPO)
[![Security](https://img.shields.io/badge/security-SECURITY.md-blue)](./SECURITY.md)
[![SBOM](https://img.shields.io/badge/SBOM-available-brightgreen)](#81-vulnerability-handling-cve-policy)
[![License](https://img.shields.io/github/license/OWNER/REPO)](./LICENSE)
[![Container](https://img.shields.io/badge/container-2--4MB-success)](#2-stated-objective-10-year-durability)
[![Helm](https://img.shields.io/badge/helm-chart-success)](#6-canonical-helm-chart-required)

A small-footprint, security-first HTTP(S) core for Kubernetes and long-lived ops. **Not nginx. Not HAProxy.** This is the smallest, most secure subset of features *fully app-integrated*—built to be **durable for 10+ years** and “30,000 GitHub star” boring.

---

## 0. Call to action

- **Minimal size, maximal performance + functionality**: scratch-style images in the **~2–4 MB** range while still being a good Kubernetes/observability citizen.
- **Keel-haul legacy services into compliance**: run Keel as a sidecar envelope around legacy HTTP/HTTPS apps to force modern security posture without rewriting the app.
- **Maximum flexibility without feature sprawl**: defaults are built-in and on; you opt out at build time to reach a smaller/stricter subset.

---

## 1. Why Golang

I picked **golang** because it hits the best “ops-to-footprint” ratio for a deployable HTTP(S) core:

- Single self-contained binary (scratch-style images, easy rollbacks).
- HTTPS in the standard library (`net/http` + `crypto/tls`).
- Mature ecosystem for routing, middleware, and observability.
- Fast builds + easy cross-compile for Linux/macOS/Windows.

### 1.1 Size + HTTPS comparison (illustrative)

These are practical “server bones” comparisons (HTTPS + minimal routing + sane defaults). Exact sizes vary by base image, libc choice, and what you link in.

| Option | Typical minimal prod container size | HTTPS/TLS story | Notes (re: this project) |
|---|---:|---|---|
| **golang** | **~2–4 MB** | Built-in `crypto/tls`; easy to make TLS1.3-only | Best “small + capable + boring” combo |
| Rust | ~5–15 MB | Strong crates (`rustls`, `hyper/axum`) | Great, but more build complexity; usually larger than golang in practice |
| Zig | ~10–30+ MB | You bring TCP/TLS/HTTP(S) plumbing | Zig is not “that small” once you add TCP/TLS/HTTP(S) support |
| Python | ~40–120+ MB | Runtime-heavy; TLS via OpenSSL | Great DX; not aligned with “tiny core” objective |
| Ruby | ~40–120+ MB | Runtime-heavy; TLS via OpenSSL | Same size story as Python |
| Node.js | ~60–150+ MB | Runtime-heavy; TLS via OpenSSL | Often the largest option |

---

## 2. Stated objective: 10+ year durability

This project optimizes for:
- **Longevity**: stable APIs, conservative dependencies, strong upgrade story.
- **Small prod footprint**: core image target **~2–4 MB** (scratch-style + CA certs where needed).
- **Security posture by default**: strict TLS policy, safe defaults, documented hardening, proactive vulnerability handling.
- **Operational excellence**: predictable behavior in Kubernetes, systemd, Windows services, and “boring” infra.

---

## 3. Default-on feature set with build-time opt-out

All major features are **built-in by default**. There is **no golang command-line feature matrix**.

- Runtime config (ports, limits, endpoints, etc.) is configured via files/env/flags as needed.
- **Feature inclusion** is controlled only at build time:
  - Defaults are **on**.
  - You **opt out** by adding build arguments (tags) to remove features you don’t want.

### 3.1 CI discipline: scripts-first
All CI must use **POSIX bash scripts** to the maximum extent possible. CI config files should not contain long chains of inline `run:` lines.

**Rule:** CI invokes comprehensive scripts from `./scripts/` (e.g., `./scripts/ci_build.sh`, `./scripts/ci_test.sh`, `./scripts/release.sh`).

### 3.2 Build-time opt-out flags (illustrative)
Build tags are **negative** (“remove X”), so defaults stay on:

- `no_otel` — remove OTLP/OpenTelemetry
- `no_prom` — remove Prometheus `/metrics`
- `no_statsd` — remove StatsD output
- `no_remotelog` — remove remote log sink support
- `no_owasp` — remove OWASP hardening middleware layer
- `no_authn` — remove authn middleware layer
- `no_sidecar` — remove sidecar reverse-proxy envelope mode
- `no_h2` — remove HTTP/2 support
- `no_h3` — remove HTTP/3 support

Example (conceptual):
```sh
golang build -tags 'no_h3,no_statsd' ./cmd/keel
```

**Size rule:** If a feature is opted out, its dependencies are not linked → no container bloat.

### 3.3 Deployment modes: library and sidecar

#### 3.3.1 Library mode (in-process)
- Your golang app links the core and registers handlers.
- Lowest latency; simplest runtime for golang services.

#### 3.3.2 Sidecar mode (envelope / reverse-proxy)
- Keel runs as a reverse-proxy sidecar in the same pod.
- Your app can stay “legacy/simple”: plain HTTP on localhost.
- Keel provides the outer contract: TLS, authn, OWASP hardening, observability, probe endpoints.

**Keel-hauling (legacy compliance):** sidecar mode is the easy way to force old HTTP/HTTPS apps into modern security compliance—*keel-hauling* them into safer defaults without rewriting the app.

### 3.4 OWASP + authn layers (default-on, opt-out)
Security posture is strict by default, with two app-integrated middleware layers:

- **OWASP layer** (default-on, opt-out with `no_owasp`)
  - canonical security headers (configurable)
  - request/response size limits
  - timeout policy

- **Authn layer** (default-on, opt-out with `no_authn`)
  - primary: **JWT**
  - plus one additional canonical mechanism (implementation-defined; e.g., OIDC discovery-backed JWT validation or mTLS identity mapping)

#### 3.4.1 Authn trust model (explicit and configurable)
- **Upstream trust** (who Keel accepts from):
  - `trusted_ids[]` (plural), and/or
  - `trusted_signers[]` (plural) (e.g., JWKs/key IDs/public keys, as supported)
  - Note: **ID != email**. IDs are stable principal identifiers (opaque strings), not contact addresses.
- **Downstream trust** (who Keel presents as):
  - `my_id` (singular), and/or
  - `my_signature_key` (singular) (private key reference via secrets)

Keel must allow enforcing trust by **ID allowlist**, **signer allowlist**, or both.

### 3.5 Limits and guardrails (configurable)

Keel must support a full set of limits via configuration (env vars and/or config file), including:
- max header bytes (aggregate)
- max header count
- max request body bytes
- max response body bytes (where applicable)
- read header timeout, read timeout, write timeout, idle timeout
- max concurrent requests / in-flight cap
- optional queue depth / backpressure behavior
- per-route overrides (implementation-defined)

#### 3.5.1 Memory backpressure (not a WAF)
Keel is **not** intended to be a full-blown WAF (no DDoS protection suite, no “rate limiting product”, no bot management).  
Instead, Keel must provide **memory backpressure** so the process degrades gracefully and avoids OOM.

- An external config variable sets an internal heap/memory limit (e.g., `heap_max_bytes`).
- When memory pressure crosses a configured threshold:
  1) **Flip readiness to false** and keep it false **until the situation resolves** (pressure drops below hysteresis threshold).  
  2) **Begin shedding load**: return **503** (service unavailable) and/or **429** (too many requests) for new work, and let upstream retry/backoff policies deal with it.

This mechanism is the safety valve; it is not a substitute for perimeter DDoS defenses.

### 3.6 Protocol support: HTTP/1.1, HTTP/2, HTTP/3
- HTTP/1.1 supported as baseline.
- **HTTP/2** is supported and enabled by default (opt-out with `no_h2`).
- **HTTP/3** support is supported where feasible and can be enabled by default or shipped as a build-time inclusion (opt-out with `no_h3`), subject to dependency/size tradeoffs.

### 3.7 TLS policy and certificates

#### 3.7.1 Default TLS stack: Google “boring SSL”
Default to a **Google “boring SSL” implementation** where available (BoringSSL / boringcrypto-backed builds), with a standard golang fallback when not selected.

#### 3.7.2 TLS 1.3 only
**TLS 1.2 is not supported.** The allowed protocol set is TLS 1.3 only.

#### 3.7.3 Build-time selectable default certificate (scratch)
For ultra-small scratch-style images, Keel supports selecting **one default certificate** at build time and embedding it into the image, so HTTPS can start without runtime filesystem dependencies.

- Intended for dev/test and “it boots” defaults.
- Production expects Kubernetes Secrets or platform cert injection.

---

## 4. Requirements: Kubernetes + observability (default-on, opt-out)

### 4.1 Kubernetes health endpoints (alt port optional)
Expose dedicated endpoints (and optionally a separate port) for:
- Liveness: `GET /healthz`
- Readiness: `GET /readyz` (may include dependency checks; in sidecar mode may include upstream reachability)
- Startup (optional): `GET /startupz`

### 4.2 Tracing (OTLP/OpenTelemetry)
- inbound request spans and context propagation
- OTLP export to collectors

### 4.3 Prometheus scraping
- `GET /metrics` in Prometheus text format
- RED metrics and in-flight tracking

### 4.4 StatsD output stream
- counters/gauges/timers to a configurable endpoint
- aligned naming conventions where possible

### 4.5 Structured logging + remote sink
- JSON logs with correlation fields (`trace_id`, `span_id`, `request_id`, etc.)
- optional remote logging sink (syslog/TCP/HTTP)

---

## 5. Extension model: add your own handlers without editing core

Keel exposes a registration surface so consumers can publish handlers without modifying Keel.

### 5.1 Route publication model
- A published route specifies **both**:
  - **port** (listener key)
  - **path** (route pattern)
- **Last registration wins** for any `(port, path)` collision. This enables “override by import order / option order” without patching Keel.
- Registrars are expected to be pure: they mount handlers and return; no background goroutines unless explicitly documented.

### 5.2 Built-in default route (no external file)
- If no user route claims `port 80` + `/`, Keel serves a built-in default response **from an embedded string** (not an external file).  
- This guarantees a deterministic “it boots” behavior for probes and early debugging even in scratch-style images.

Example (conceptual):

```golang
server := NewServer(
  WithConfig(cfg),
  WithRegistrar(myapi.NewRegistrar()),     // your routes
  WithRegistrar(admin.NewRegistrar()),     // optional extra routes
)
server.Run()
```

---

## 6. Canonical Helm chart (required)

This repo must ship a canonical Helm chart that is the reference deployment for “works in Kubernetes”.

### 6.1 Helm must support image flavors and opt-out alignment
- `image.flavor: default|minimal|...` (implementation-defined)
- Chart values must align with the chosen build flavor; misconfig should fail fast or warn loudly.

### 6.2 Helm must support both deployment modes
- library mode (single container)
- sidecar mode (two containers: `app` + `keel`) where Keel envelopes the app

### 6.3 Scaling signals (via metrics)
Keel’s memory backpressure exposes clear signals for external autoscalers:
- readiness flip (immediate traffic reduction)
- metrics for pressure and shedding (for HPA/KEDA via Prometheus adapters)

Autoscaling policy (HPA/KEDA/VPA) remains external and declarative; Keel only emits the signals.

---

## 7. Full cross-platform graceful lifecycle

### 7.1 Common behaviors (all platforms)
- stop accepting new connections
- drain in-flight requests (bounded timeout)
- flush logs/metrics/traces
- close listeners and background workers cleanly
- idempotent shutdown

### 7.2 POSIX / macOS signal support
- `SIGTERM`, `SIGINT` => graceful shutdown
- `SIGHUP` => config reload (if enabled) or no-op with log
- optional: `SIGQUIT`, `SIGUSR1/2` for diagnostics

### 7.3 Windows process-control support
- handle Windows console control events: `CTRL_C_EVENT`, `CTRL_BREAK_EVENT`, and close/logoff/shutdown where supported
- map to the same shutdown orchestrator

---

## 8. Security governance and vulnerability handling

### 8.1 Vulnerability handling (CVE policy)
- Publish **SECURITY.md**:
  - private reporting channel
  - triage + acknowledgment expectations
  - supported versions window / LTS policy
  - coordinated disclosure policy
- Maintain **CHANGELOG** with security fixes clearly marked.
- Ship SBOM/provenance in CI and document it.

### 8.2 Release discipline
- semantic versioning
- API stability guarantees + deprecation policy
- CI gates: tests, lint, static analysis, minimal integration tests (Kubernetes + Windows shutdown)

---

## 9. What this is not
- Not a generic L7 proxy buffet.
- Not a “kitchen sink” mesh replacement.
- Not nginx/haproxy.
- Not a full-blown WAF (no DDoS protection suite, no rate-limiting product, no bot management, no deep packet inspection).

Keel provides a **minimal, security-first subset** of features that are fully app-integrated. Where overload protection is required, Keel uses **memory backpressure** (readiness flip + 503/429 shedding) to prevent OOM and degrade predictably.
