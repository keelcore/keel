# HTTP(S) Server Kit (Golang)

[![CI](https://img.shields.io/github/actions/workflow/status/OWNER/REPO/ci.yml?branch=main)](https://github.com/OWNER/REPO/actions)
[![Release](https://img.shields.io/github/v/release/OWNER/REPO)](https://github.com/OWNER/REPO/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/OWNER/REPO.svg)](https://pkg.go.dev/github.com/OWNER/REPO)
[![Go Report Card](https://goreportcard.com/badge/github.com/OWNER/REPO)](https://goreportcard.com/report/github.com/OWNER/REPO)
[![Coverage](https://img.shields.io/codecov/c/github/OWNER/REPO)](https://codecov.io/gh/OWNER/REPO)
[![Security](https://img.shields.io/badge/security-SECURITY.md-blue)](./SECURITY.md)
[![SBOM](https://img.shields.io/badge/SBOM-available-brightgreen)](#81-vulnerability-handling-cve-policy)
[![License](https://img.shields.io/github/license/OWNER/REPO)](./LICENSE)
[![Container](https://img.shields.io/badge/container-5--8MB-success)](#2-stated-objective-10-year-durability)
[![Helm](https://img.shields.io/badge/helm-chart-success)](#6-canonical-helm-chart)

A small-footprint, security-first HTTP(S) core for Kubernetes and long-lived ops. **Not nginx. Not HAProxy.** This is the smallest, most secure subset of features *fully app-integrated*—built to be **durable for 10+ years** and a goal of "30,000 GitHub star" boring.

---

## 0. Call to action

- **Minimal size, maximal performance + functionality**: scratch-style images in the **~5–8 MB** range while still being a good Kubernetes/observability citizen.
- **Keel-haul legacy services into compliance**: run Keel as a sidecar envelope around legacy HTTP/HTTPS apps to force modern security posture without rewriting the app.
- **Maximum flexibility without feature sprawl**: defaults are built-in and on; you opt out at build time to reach a smaller/stricter subset.
- **Build on top, not alongside**: use Keel as a Go library to build your own service with production-grade TLS, authn, observability, and lifecycle already wired in.

---

## 1. Why Golang

We picked **golang** because it hits the best "ops-to-footprint" ratio for a deployable HTTP(S) core:

- Single self-contained binary (scratch-style images, easy rollbacks).
- HTTPS in the standard library (`net/http` + `crypto/tls`).
- Mature ecosystem for routing, middleware, and observability.
- Fast builds + easy cross-compile for Linux/macOS/Windows.

### 1.1 Size + HTTPS comparison (illustrative)

| Option | Typical minimal prod container size | HTTPS/TLS story | Notes |
|---|------------------------------------:|---|---|
| **golang** | **~5-8 MB** | Built-in `crypto/tls`; easy to make TLS1.3-only | Best "small + capable + boring" combo |
| Rust | ~5–15 MB | Strong crates (`rustls`, `hyper/axum`) | Great, but more build complexity |
| Zig | ~10–30+ MB | You bring TCP/TLS/HTTP(S) plumbing | Not "that small" once you add HTTP(S) |
| Python | ~40–120+ MB | Runtime-heavy; TLS via OpenSSL | Great DX; not aligned with "tiny core" |
| Ruby | ~40–120+ MB | Runtime-heavy; TLS via OpenSSL | Same size story as Python |
| Node.js | ~60–150+ MB | Runtime-heavy; TLS via OpenSSL | Often the largest option |

---

## 2. Stated objective: 10+ year durability

This project optimizes for:
- **Longevity**: stable APIs, conservative dependencies, strong upgrade story.
- **Small prod footprint**: core image target **~5–8 MB** (scratch-style + CA certs where needed).
- **Security posture by default**: TLS 1.3-only, safe defaults, documented hardening, proactive vulnerability handling.
- **Operational excellence**: predictable behavior in Kubernetes, systemd, Windows services, and "boring" infra.

---

## 3. Default-on feature set with build-time opt-out

All major features are **built-in by default**. There is **no command-line feature matrix**.

- Runtime config is via YAML file, ENV vars, and secrets file (see §3.8).
- **Feature inclusion** is controlled only at build time via negative tags (defaults are **on**, you opt **out**).

### 3.1 CI discipline: scripts-first

All CI must use provided **POSIX bash scripts** to the maximum extent possible. CI config files must not contain long chains of inline `run:` lines.

**Rule:** CI invokes comprehensive scripts from `./scripts/` (e.g., `./scripts/ci_build.sh`, `./scripts/ci_test.sh`, `./scripts/release.sh`).

### 3.2 Build-time opt-out flags

Build tags are **negative** ("remove X"), so defaults stay on:

| Tag | Removes |
|---|---|
| `no_otel` | OTLP/OpenTelemetry tracing |
| `no_prom` | Prometheus `/metrics` |
| `no_statsd` | StatsD output |
| `no_remotelog` | Remote log sink support |
| `no_owasp` | OWASP hardening middleware layer |
| `no_authn` | Authn middleware layer |
| `no_sidecar` | Sidecar reverse-proxy envelope mode |
| `no_h2` | HTTP/2 support |
| `no_h3` | HTTP/3 support |
| `no_acme` | ACME/Let's Encrypt certificate management |

Example:
```sh
go build -tags 'no_h3,no_statsd' ./cmd/keel
```

**Size rule:** If a feature is opted out, its dependencies are not linked → no container bloat.

### 3.3 Deployment modes

#### 3.3.1 Library mode (in-process)

Your Go app links Keel and registers handlers. Lowest latency; simplest runtime for Go services.

```go
srv := keel.New(
    keel.WithConfig(cfg),
    keel.WithRoute(ports.HTTP, "/api/v1/", myapi.Handler()),
    keel.WithRoute(ports.HTTP, "/api/v1/health", myapi.HealthHandler()),
)
srv.Run(ctx)
```

Keel exports its middleware pipeline so application code can compose it independently:

```go
import "github.com/keelcore/keel/pkg/core/mw"

// Apply individual middleware to your own handler.
h := mw.OWASP(cfg, mw.RequestID(myHandler))
```

Context keys for request metadata injected by Keel:

```go
import "github.com/keelcore/keel/pkg/core/ctxkeys"

requestID := r.Context().Value(ctxkeys.RequestID).(string)
traceID   := r.Context().Value(ctxkeys.TraceID).(string)
```

#### 3.3.2 Sidecar mode (envelope / reverse-proxy)

Keel runs as a sidecar and proxies to an upstream service. Two upstream topologies are explicitly supported:

**Intra-pod (localhost upstream):** Keel and the app share a pod. The app listens on `localhost:<port>` over plain HTTP; Keel owns all external-facing ports and applies TLS, authn, OWASP hardening, observability, and backpressure. The pod network namespace is the trust boundary — no TLS is needed on the loopback leg.

**Out-of-pod (remote upstream):** Keel proxies to a service running outside the pod — a legacy VM, a third-party API endpoint, or a service in another namespace not covered by a service mesh. In this topology Keel establishes a TLS or mTLS connection to the upstream, presenting a client certificate if the upstream requires mutual authentication (see §8.6).

**Keel-hauling:** both topologies allow forcing old HTTP/HTTPS services into modern security compliance without rewriting them.

See §8 for full sidecar behavior specification.

#### 3.3.3 ACME edge mode (standalone TLS terminator)

When ACME is enabled, Keel manages its own certificate via Let's Encrypt (or any ACME-compatible CA) with automatic renewal.

**Critical constraint:** The ACME http-01 challenge requires a route at:

```
GET http://<domain>/.well-known/acme-challenge/<token>
```

This route **must be served over plain HTTP on port 80**, even if Keel redirects all other HTTP traffic to HTTPS. Keel handles this automatically:

1. Plain HTTP listener on port 80 is kept alive.
2. `/.well-known/acme-challenge/` path is registered **before** any redirect or authn middleware.
3. All other HTTP paths are 301-redirected to HTTPS.
4. Certificate renewal is automatic; Keel reloads the cert without restart or dropped connections.

ACME must not be combined with `no_acme` build tag. ACME requires `cert_file`/`key_file` to be left empty (Keel manages them).

### 3.4 OWASP + authn layers (default-on, opt-out)

#### 3.4.1 OWASP middleware (default-on, opt-out with `no_owasp`)

Applied to all main-port traffic. Enforces:

- Canonical security headers on every response:
  - `x-content-type-options: nosniff`
  - `x-frame-options: DENY`
  - `referrer-policy: no-referrer`
  - `content-security-policy: default-src 'none'`
  - `permissions-policy: geolocation=()`
  - `strict-transport-security: max-age=63072000; includeSubDomains` (HTTPS only)
- Request body size limit (`max_request_body_bytes`)
- Response body size cap (`max_response_body_bytes`, where applicable)
- Max header bytes (`max_header_bytes`)
- Per-request timeout enforcement

Headers are configurable overrides; the defaults are maximally restrictive.

#### 3.4.2 Authn layer (default-on, opt-out with `no_authn`)

Two supported mechanisms:

**Primary: JWT bearer token**
- HS256, RS256, ES256 all supported.
- `trusted_signers` may be bare secret keys (HS256), PEM public keys (RS256/ES256), or JWKs endpoint URLs (key fetched and cached with TTL).
- `trusted_ids` enforces subject (`sub`) allowlist; empty = any valid signed token is accepted.

**Secondary: mTLS client certificate identity mapping**
- Client cert presented at TLS handshake.
- Subject CN or SAN mapped to a principal ID.
- Same `trusted_ids` allowlist applies.

#### 3.4.3 Authn trust model

- **Upstream trust** (who Keel accepts from):
  - `trusted_ids[]` — stable principal identifiers (opaque strings, not email addresses).
  - `trusted_signers[]` — keys, PEM certs, or JWKs URLs.
- **Downstream trust** (who Keel presents as):
  - `my_id` — principal identifier asserted in outbound `sub` claim.
  - `my_signature_key_file` — path to private key used to sign outbound JWT on forwarded requests (sidecar mode).

Keel enforces trust by **ID allowlist**, **signer allowlist**, or both.

### 3.5 Limits and guardrails (configurable)

All limits are configurable via YAML or ENV. Per-route overrides are implementation-defined.

| Limit | Config key | ENV var | Default |
|---|---|---|---|
| Max header bytes (aggregate) | `security.max_header_bytes` | `KEEL_MAX_HEADER_BYTES` | 65536 |
| Max request body bytes | `security.max_request_body_bytes` | `KEEL_MAX_REQ_BODY_BYTES` | 10485760 |
| Max response body bytes | `security.max_response_body_bytes` | `KEEL_MAX_RESP_BODY_BYTES` | 52428800 |
| Read header timeout | `timeouts.read_header` | `KEEL_READ_HEADER_TIMEOUT` | 5s |
| Read timeout | `timeouts.read` | `KEEL_READ_TIMEOUT` | 30s |
| Write timeout | `timeouts.write` | `KEEL_WRITE_TIMEOUT` | 30s |
| Idle timeout | `timeouts.idle` | `KEEL_IDLE_TIMEOUT` | 60s |
| Shutdown drain timeout | `timeouts.shutdown_drain` | `KEEL_SHUTDOWN_DRAIN` | 10s |
| Pre-stop sleep | `timeouts.prestop_sleep` | `KEEL_PRESTOP_SLEEP` | 0s |
| Max concurrent requests | `limits.max_concurrent` | `KEEL_MAX_CONCURRENT` | 0 (unlimited) |
| Queue depth | `limits.queue_depth` | `KEEL_QUEUE_DEPTH` | 0 (no queue) |

#### 3.5.1 Memory backpressure (not a WAF)

Keel is **not** a full-blown WAF. Instead, it provides **memory backpressure** so the process degrades gracefully and avoids OOM.

- `backpressure.heap_max_bytes` sets the internal heap limit.
- When heap pressure crosses `high_watermark`:
  1. **Flip readiness to false** and hold until pressure drops below `low_watermark`.
  2. **Shed load**: return **503** for new requests. With a queue configured, return **429** when queue is full.
- `keel_memory_pressure` Prometheus gauge exposes the current pressure ratio.

This is the safety valve; it is not a substitute for perimeter DDoS defenses.

#### 3.5.2 Concurrency cap

When `limits.max_concurrent > 0`, Keel uses a semaphore to cap in-flight requests. Requests over the cap are either queued (if `limits.queue_depth > 0`) or immediately rejected with **429**. Queued requests that exceed `timeouts.write` are rejected with **503**.

#### 3.5.3 Pre-stop drain (Kubernetes rolling deploys)

Kubernetes removes a pod from endpoints before sending SIGTERM, but there is a propagation race. Setting `timeouts.prestop_sleep` (e.g., `5s`) causes Keel to sleep that duration before beginning drain, ensuring all in-flight proxy traffic has been rerouted. This eliminates 502s during rolling deploys.

### 3.6 Protocol support: HTTP/1.1, HTTP/2, HTTP/3

- HTTP/1.1 supported as baseline.
- **HTTP/2** is supported and enabled by default (opt-out with `no_h2`).
- **HTTP/3** (QUIC) supported and enabled by default (opt-out with `no_h3`), subject to dependency/size tradeoffs. Requires UDP.

### 3.7 TLS policy and certificates

#### 3.7.1 Default TLS stack: Google "boring SSL"

Default to a **Google "boring SSL" implementation** where available (BoringSSL / boringcrypto-backed builds), with a standard Go fallback when not selected.

#### 3.7.2 TLS 1.3 only

**TLS 1.2 is not supported.** The minimum and only allowed protocol version is **TLS 1.3**.

Cipher suites are not configurable; TLS 1.3 mandates its own suite selection. In FIPS mode the BoringCrypto FIPS-approved TLS 1.3 suites are used.

#### 3.7.3 Build-time selectable default certificate

For scratch-style images, Keel supports selecting **one default certificate** at build time and embedding it into the binary, so HTTPS can start without runtime filesystem dependencies.

- Intended for dev/test and "it boots" defaults.
- Production uses Kubernetes Secrets, platform cert injection, or ACME.

#### 3.7.4 ACME / Let's Encrypt

When `tls.acme.enabled: true`:

- `tls.cert_file` and `tls.key_file` must be empty (ACME manages them).
- `tls.acme.domains[]` lists the SANs to certify.
- `tls.acme.cache_dir` is the on-disk cert cache (use a PVC or emptyDir in k8s).
- `tls.acme.email` is the ACME account email.
- `tls.acme.ca_url` defaults to Let's Encrypt production; override for staging or internal CA.
- Renewals happen automatically without restart.
- The HTTP-01 challenge route is registered automatically on the HTTP listener before any authn or redirect middleware.

### 3.8 Configuration

#### 3.8.1 YAML config file

Keel loads its primary config from a YAML file. The path is set via `--config` flag or `KEEL_CONFIG` env var. If not set, Keel starts with built-in defaults and ENV overrides only.

Full schema with defaults:

```yaml
# keel.yaml

listeners:
  http:
    enabled: true
    port: 8080
  https:
    enabled: false
    port: 8443
  h3:
    enabled: false
    port: 8443      # shares port with https (UDP)
  health:
    enabled: true
    port: 9091
  ready:
    enabled: true
    port: 9092
  startup:
    enabled: false
    port: 9093
  admin:
    enabled: false
    port: 9999      # /version, /debug/pprof, /admin/reload, /metrics/fips

tls:
  cert_file: ""   # leave empty when using ACME
  key_file: ""
  acme:
    enabled: false
    domains: []
    email: ""
    cache_dir: /var/lib/keel/acme
    ca_url: ""    # default: Let's Encrypt production

sidecar:
  enabled: false
  upstream_url: http://127.0.0.1:3000   # localhost for intra-pod; https://host:port for remote
  upstream_health_path: /health
  upstream_health_interval: 10s
  upstream_health_timeout: 2s
  upstream_tls:
    enabled: false               # set true when upstream_url is https://
    ca_file: ""                  # CA cert to verify upstream's server cert (PEM); empty = system roots
    client_cert_file: ""         # client cert for mTLS (PEM); empty = no client cert
    client_key_file: ""          # client private key for mTLS (PEM)
    insecure_skip_verify: false  # never true in production
  circuit_breaker:
    enabled: true
    failure_threshold: 5
    reset_timeout: 30s
  header_policy:
    forward: []        # explicit allowlist; empty = forward all non-hop-by-hop
    strip: []          # headers to always remove before forwarding
  xff_mode: append     # append | replace | strip
  xff_trusted_hops: 0  # how many rightmost XFF entries to trust from upstream

security:
  owasp_headers: true
  max_header_bytes: 65536
  max_request_body_bytes: 10485760
  max_response_body_bytes: 52428800
  hsts_max_age: 63072000      # two years; applied on HTTPS listeners only

timeouts:
  read_header: 5s
  read: 30s
  write: 30s
  idle: 60s
  shutdown_drain: 10s
  prestop_sleep: 0s

limits:
  max_concurrent: 0   # 0 = unlimited
  queue_depth: 0      # 0 = shed immediately; >0 = queue up to N, then 429

backpressure:
  heap_max_bytes: 0   # 0 = disabled
  high_watermark: 0.85
  low_watermark: 0.70
  shedding_enabled: true

authn:
  enabled: true
  trusted_ids: []
  trusted_signers: []     # keys, PEM pubkeys, or JWKs URLs; all tried in order
  trusted_signers_file: ""  # path to file with one entry per line (secrets mount)
  my_id: ""
  my_signature_key_file: ""  # path to private key for outbound JWT signing

logging:
  json: true
  level: info
  access_log: true       # per-request structured access log line
  remote_sink:
    enabled: false
    endpoint: ""
    protocol: http       # http | syslog

metrics:
  prometheus: true       # /metrics on admin port (or main port if admin disabled)
  statsd:
    enabled: false
    endpoint: ""
    prefix: keel

tracing:
  otlp:
    enabled: false
    endpoint: ""         # e.g., otel-collector:4317
    insecure: false

fips:
  monitor: true          # expose /health/fips and keel_fips_active metric
```

#### 3.8.2 ENV var overrides

Every scalar config value has a corresponding `KEEL_*` ENV var. ENV vars override YAML file values. See §3.5 for the full limit table; other key vars:

| ENV var | YAML path | Default |
|---|---|---|
| `KEEL_CONFIG` | — | `""` |
| `KEEL_SECRETS` | — | `""` |
| `KEEL_HTTP_ENABLED` | `listeners.http.enabled` | `true` |
| `KEEL_HTTP_PORT` | `listeners.http.port` | `8080` |
| `KEEL_HTTPS_ENABLED` | `listeners.https.enabled` | `false` |
| `KEEL_HTTPS_PORT` | `listeners.https.port` | `8443` |
| `KEEL_H3_ENABLED` | `listeners.h3.enabled` | `false` |
| `KEEL_HEALTH_PORT` | `listeners.health.port` | `9091` |
| `KEEL_READY_PORT` | `listeners.ready.port` | `9092` |
| `KEEL_STARTUP_PORT` | `listeners.startup.port` | `9093` |
| `KEEL_ADMIN_PORT` | `listeners.admin.port` | `9999` |
| `KEEL_TLS_CERT` | `tls.cert_file` | `""` |
| `KEEL_TLS_KEY` | `tls.key_file` | `""` |
| `KEEL_SIDECAR` | `sidecar.enabled` | `false` |
| `KEEL_UPSTREAM_URL` | `sidecar.upstream_url` | `""` |
| `KEEL_UPSTREAM_TLS` | `sidecar.upstream_tls.enabled` | `false` |
| `KEEL_UPSTREAM_CA_FILE` | `sidecar.upstream_tls.ca_file` | `""` |
| `KEEL_UPSTREAM_CLIENT_CERT` | `sidecar.upstream_tls.client_cert_file` | `""` |
| `KEEL_UPSTREAM_CLIENT_KEY` | `sidecar.upstream_tls.client_key_file` | `""` |
| `KEEL_OWASP` | `security.owasp_headers` | `true` |
| `KEEL_AUTHN` | `authn.enabled` | `true` |
| `KEEL_TRUSTED_IDS` | `authn.trusted_ids` | `""` (comma-separated) |
| `KEEL_TRUSTED_SIGNERS` | `authn.trusted_signers` | `""` (comma-separated) |
| `KEEL_MY_ID` | `authn.my_id` | `""` |
| `KEEL_LOG_JSON` | `logging.json` | `true` |
| `KEEL_SHEDDING` | `backpressure.shedding_enabled` | `true` |
| `KEEL_HEAP_MAX_BYTES` | `backpressure.heap_max_bytes` | `0` |

#### 3.8.3 Secrets (YAML secrets file / k8s Secret bind mount)

Secrets are kept out of the primary config file. A separate **secrets YAML file** is loaded from the path set by `--secrets` flag or `KEEL_SECRETS` env var.

```yaml
# keel-secrets.yaml  (bind-mounted from a k8s Secret)
tls:
  cert_file: /etc/keel/tls/tls.crt
  key_file: /etc/keel/tls/tls.key

authn:
  trusted_signers:
    - /etc/keel/secrets/signer-a.pem
    - /etc/keel/secrets/signer-b.pem
  my_signature_key_file: /etc/keel/secrets/my-signing.key
```

Secrets file values override matching YAML config values. Values that are file paths (ending in `.pem`, `.crt`, `.key`, or explicitly declared as `*_file` keys) are read from disk at startup and on SIGHUP reload.

**k8s pattern:**
```yaml
# values.yaml (Helm)
secrets:
  existingSecret: my-keel-secrets   # k8s Secret name
  mountPath: /etc/keel/secrets
```

The Helm chart mounts the k8s Secret at `mountPath` and sets `KEEL_SECRETS` to point to `keel-secrets.yaml` within that mount.

#### 3.8.4 Config validation

Keel validates config at startup and fails fast with a clear error if:
- HTTPS or H3 is enabled but no cert/key is provided and ACME is not enabled.
- ACME is enabled but cert/key paths are also set.
- ACME domains list is empty with ACME enabled.
- `high_watermark` ≤ `low_watermark`.
- An unreadable secrets file is referenced.
- An unknown build-tag feature is enabled in config but was compiled out.

Use `--validate` to check config without starting listeners.

### 3.9 SIGHUP hot reload

On `SIGHUP`, Keel:

1. Re-reads the YAML config file and secrets file from disk.
2. Validates the new config (rejects and logs on error; keeps running with old config).
3. Applies changes to: log level, authn keys, trusted IDs, pressure limits, upstream URL.
4. For TLS cert/key changes: performs a zero-downtime cert reload (new TLS handshakes use new cert; in-flight TLS sessions are not disrupted).
5. Does **not** change listener ports or protocol bindings (requires restart).

`/admin/reload` provides an HTTP alternative to SIGHUP for environments that cannot send signals (Windows, some container runtimes). Requires admin port to be enabled and authn to pass.

---

## 4. Kubernetes + observability (default-on, opt-out)

### 4.1 Kubernetes health endpoints

Dedicated endpoints on separate ports (never exposed on main traffic port):

| Endpoint | Port | Purpose |
|---|---|---|
| `GET /healthz` | `health` | Liveness: is the process alive? Always 200 if running. |
| `GET /readyz` | `ready` | Readiness: is the process ready for traffic? 503 when shedding or startup incomplete. |
| `GET /startupz` | `startup` | Startup: has the process finished initializing? 503 until ready. Prevents liveness from killing a slow-starting app. |

In sidecar mode, `/readyz` also checks upstream reachability (via upstream health check); fails if upstream is unhealthy.

Probe ports are hit by kubelet directly on PodIP; they do not need a Service. See §6 for Helm chart probe configuration.

### 4.2 Tracing (OTLP/OpenTelemetry)

- Inbound request spans with `trace_id`, `span_id`, `parent_span_id`.
- Context propagation via `traceparent` / `tracestate` (W3C Trace Context).
- OTLP gRPC export to a configurable collector endpoint.
- Span attributes: `http.method`, `http.route`, `http.status_code`, `http.request_content_length`, upstream latency (sidecar mode).
- Opt-out: `no_otel` build tag.

### 4.3 Prometheus scraping

Exposed on the admin port (or main port if admin is disabled). Path: `GET /metrics`.

Core metrics:

| Metric | Type | Description |
|---|---|---|
| `keel_requests_total` | Counter | Requests by method, route, status code |
| `keel_request_duration_seconds` | Histogram | End-to-end latency |
| `keel_requests_inflight` | Gauge | Current in-flight request count |
| `keel_requests_shed_total` | Counter | Requests rejected due to shedding (503/429) |
| `keel_memory_pressure` | Gauge | Current heap pressure ratio (0.0–1.0) |
| `keel_upstream_health` | Gauge | 1=healthy, 0=unhealthy (sidecar mode) |
| `keel_circuit_open` | Gauge | 1=circuit open (shedding upstream) (sidecar) |
| `keel_tls_cert_expiry_seconds` | Gauge | Seconds until TLS cert expiry |
| `keel_fips_active` | Gauge | 1 if FIPS mode is active, 0 otherwise |

Opt-out: `no_prom` build tag.

### 4.4 StatsD output stream

Counters, gauges, and timers emitted to a configurable UDP endpoint. Naming convention aligns with Prometheus metric names where possible (`.` separator). Opt-out: `no_statsd` build tag.

### 4.5 Structured logging

All log output is JSON by default (`logging.json: true`). Fields on every line:

```json
{"ts":"2026-01-01T00:00:00Z","level":"info","msg":"listener_up","addr":":8080","tls":false,"proto":"http/1.1"}
```

**Access log** (one line per request, `logging.access_log: true`):

```json
{
  "ts": "2026-01-01T00:00:00.123Z",
  "level": "info",
  "msg": "access",
  "request_id": "01J...",
  "trace_id": "4bf92f3577b34da6...",
  "span_id": "00f067aa0ba902b7",
  "method": "GET",
  "path": "/api/v1/items",
  "status": 200,
  "bytes_in": 0,
  "bytes_out": 512,
  "duration_ms": 4.2,
  "upstream_duration_ms": 3.1,
  "client_ip": "10.0.0.1",
  "user_agent": "keel-client/1.0"
}
```

**Request ID:** Keel reads `X-Request-ID` from inbound requests. If absent, it generates a new ID (ULID). The ID is propagated to the upstream in sidecar mode and injected into the response as `X-Request-ID`.

**Remote log sink** (opt-out: `no_remotelog`): structured logs can be shipped to an HTTP endpoint or syslog/TCP receiver. Buffered and non-blocking; dropped on buffer overflow with a counter.

### 4.6 Operational endpoints (admin port)

These are served **only on the admin port** (`listeners.admin`, default port 9999). Do not expose the admin port outside the cluster.

| Endpoint | Description |
|---|---|
| `GET /version` | JSON: binary version, build tags, FIPS mode, Go version, start time |
| `GET /health/fips` | JSON: `{"fips_active": true/false}` |
| `GET /metrics` | Prometheus metrics (also available here if admin enabled) |
| `GET /debug/pprof/` | Go pprof endpoints (CPU, heap, goroutine, trace) |
| `POST /admin/reload` | Trigger config + secrets reload (equivalent to SIGHUP) |

`/version` example response:
```json
{
  "version": "0.5.0",
  "build_tags": ["no_h3", "no_statsd"],
  "fips_active": false,
  "go_version": "go1.25.5",
  "started_at": "2026-01-01T00:00:00Z"
}
```

### 4.7 Readiness dependency registration

Application code can register named readiness checks that participate in `/readyz`:

```go
srv := keel.New(
    keel.WithReadinessCheck("db", func(ctx context.Context) error {
        return db.PingContext(ctx)
    }),
    keel.WithReadinessCheck("cache", func(ctx context.Context) error {
        return cache.Ping(ctx)
    }),
)
```

All checks must pass for `/readyz` to return 200. Failed check names are included in the 503 response body for debugging.

### 4.8 SLO signals

Keel emits signals suitable for external SLO/error-budget tracking:

- `keel_requests_total{status=~"5.."}` / `keel_requests_total` = error rate.
- `keel_request_duration_seconds` histograms for latency SLOs.
- Readiness flip is observable via Prometheus (scrape `/readyz` as a blackbox target or read `keel_memory_pressure`).

Autoscaling policy (HPA/KEDA) remains external; Keel only emits signals.

---

## 5. Extension model: add your own handlers without editing core

### 5.1 Route registration

A published route specifies both **port** (listener key) and **path** (route pattern). **Last registration wins** for any `(port, path)` collision. This enables "override by import order / option order" without patching Keel.

Registrars are expected to be pure: they mount handlers and return; no background goroutines unless explicitly documented.

```go
srv := keel.New(
    keel.WithConfig(cfg),
    keel.WithRegistrar(myapi.NewRegistrar()),   // your routes
    keel.WithRegistrar(admin.NewRegistrar()),   // optional extra routes
    keel.WithRoute(ports.HTTP, "/ping", pingHandler),  // inline single route
)
srv.Run(ctx)
```

### 5.2 Built-in default route

If no user route claims `port 80` + `/`, Keel serves a built-in default response from an embedded string (not an external file). This guarantees deterministic "it boots" behavior for probes and early debugging in scratch-style images.

### 5.3 Middleware export

Individual middleware is exported for use in your own handler chains:

```go
import "github.com/keelcore/keel/pkg/core/mw"
import "github.com/keelcore/keel/pkg/core/ctxkeys"

// Compose middleware yourself.
h := mw.RequestID(         // inject/propagate X-Request-ID
     mw.AccessLog(logger,  // per-request structured log
     mw.OWASP(cfg,         // security headers + body limits
     yourHandler)))
```

### 5.4 Admin reload endpoint

`POST /admin/reload` (admin port only) triggers the same hot reload as SIGHUP. Requires authn if `authn.enabled: true`. Returns 200 on success or 422 with error detail if the new config is invalid (current config remains active).

---

## 6. Canonical Helm chart

This repo ships a canonical Helm chart (`helm/keel/`) as the reference deployment for "works in Kubernetes".

### 6.1 Image flavors and opt-out alignment

```yaml
image:
  flavor: default   # default | min | fips | custom
```

| Flavor | Build tags | Notes |
|---|---|---|
| `default` | none | All features; ~5–8 MB |
| `min` | `no_otel,no_statsd,no_remotelog,no_h3` | Minimal; ~3–4 MB |
| `fips` | FIPS boringcrypto build | For compliance environments |
| `custom` | user-defined | Set `image.repository` + `image.tag` directly |

Chart values that reference disabled features emit a `helm lint` warning. Misconfigured combos (e.g., ACME enabled + cert_file set) cause a template error.

### 6.2 Deployment modes

**Library mode** (single container):
```yaml
mode: library
```

**Sidecar mode — intra-pod** (two containers sharing pod network):
```yaml
mode: sidecar
sidecar:
  app:
    image: mycompany/myapp:1.0
    port: 3000
  upstream_url: http://127.0.0.1:3000
```

The app container listens on `localhost:3000`; Keel listens on external ports and proxies through. No upstream TLS needed.

**Sidecar mode — out-of-pod** (Keel connects to a remote upstream over mTLS):
```yaml
mode: sidecar
sidecar:
  upstream_url: https://legacy-api.internal:8443
  upstream_tls:
    enabled: true
    ca_file: /etc/keel/secrets/upstream-ca.crt
    client_cert_file: /etc/keel/secrets/client.crt
    client_key_file: /etc/keel/secrets/client.key
  upstreamTLSSecret: my-upstream-mtls-secret   # k8s Secret with ca.crt, tls.crt, tls.key
```

The chart mounts `upstreamTLSSecret` at `/etc/keel/secrets/` and sets the `upstream_tls.*_file` paths accordingly.

### 6.3 Full values reference (abbreviated)

```yaml
replicaCount: 1
mode: library      # library | sidecar

image:
  repository: keelcore/keel
  tag: "0.5.0"
  flavor: default
  pullPolicy: IfNotPresent

keel:
  config: {}          # inline keel.yaml overrides (merged with chart defaults)
  secrets:
    existingSecret: ""   # k8s Secret name to mount as secrets file
    mountPath: /etc/keel/secrets

  listeners:
    http:    { enabled: true,  port: 8080 }
    https:   { enabled: false, port: 8443 }
    h3:      { enabled: false, port: 8443 }
    health:  { enabled: true,  port: 9091 }
    ready:   { enabled: true,  port: 9092 }
    startup: { enabled: false, port: 9093 }
    admin:   { enabled: false, port: 9999 }

  tls:
    certSecretName: ""   # k8s Secret with tls.crt/tls.key
    acme:
      enabled: false
      domains: []
      email: ""
      cachePVC: ""       # PVC name for ACME cert cache

  sidecar:
    upstreamURL: ""
    upstreamTLSSecret: ""      # k8s Secret with keys: ca.crt, tls.crt, tls.key
    upstreamTLSInsecureSkipVerify: false

  authn:
    enabled: true
    trustedIDs: []
    trustedSigners: []

  backpressure:
    heapMaxBytes: 0
    highWatermark: 0.85
    lowWatermark: 0.70
    sheddingEnabled: true

  metrics:
    prometheus: true
    statsd:
      enabled: false
      endpoint: ""

  tracing:
    otlp:
      enabled: false
      endpoint: ""

  extraEnv: []
  extraVolumeMounts: []
  extraVolumes: []

serviceMonitor:
  enabled: false
  namespace: ""
  interval: 30s
  scrapeTimeout: 10s
  labels: {}

networkPolicy:
  enabled: false
  ingressRules: []
  egressRules: []

podDisruptionBudget:
  enabled: false
  minAvailable: 1

terminationGracePeriodSeconds: 30

podSecurityContext:
  runAsNonRoot: true
  runAsUser: 65532
  runAsGroup: 65532
  seccompProfile:
    type: RuntimeDefault

securityContext:
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  capabilities:
    drop: ["ALL"]
```

### 6.4 ServiceMonitor (Prometheus Operator)

When `serviceMonitor.enabled: true`, the chart creates a `ServiceMonitor` CR that scrapes `/metrics` from the admin port. Requires Prometheus Operator CRDs to be installed.

### 6.5 NetworkPolicy

When `networkPolicy.enabled: true`, the chart creates a `NetworkPolicy` that:
- Allows ingress to main ports from within the cluster.
- Allows ingress to probe ports from the kubelet CIDR only.
- Allows ingress to the admin port from a configurable namespace selector.
- Denies all other ingress by default.

### 6.6 PodDisruptionBudget

When `podDisruptionBudget.enabled: true`, the chart creates a `PodDisruptionBudget` ensuring at least `minAvailable` pods remain during voluntary disruptions (cluster upgrades, drains).

### 6.7 Scaling signals

Keel's readiness flip and Prometheus metrics provide clean signals for external autoscalers:

- Readiness flip → immediate traffic reduction (works with standard HPA).
- `keel_memory_pressure` → KEDA ScaledObject trigger via Prometheus adapter.
- `keel_requests_inflight` → scaling on concurrency rather than CPU.

---

## 7. Full cross-platform graceful lifecycle

### 7.1 Common behaviors (all platforms)

1. (Optional) Pre-stop sleep (`prestop_sleep`) to allow endpoint propagation.
2. Stop accepting new connections on all main listeners.
3. Drain in-flight requests (bounded by `shutdown_drain` timeout).
4. Flush logs, metrics, and traces.
5. Close listeners and background workers cleanly.
6. Exit 0 on clean shutdown, non-zero on error.
7. Idempotent: multiple signals or concurrent shutdown calls are safe.

### 7.2 POSIX / macOS signal support

| Signal | Behavior |
|---|---|
| `SIGTERM` | Graceful shutdown |
| `SIGINT` | Graceful shutdown |
| `SIGHUP` | Config + secrets reload (see §3.9) |
| `SIGQUIT` | Dump goroutine stack trace to stderr, then graceful shutdown |
| `SIGUSR1` | Log current config to stderr (diagnostic) |
| `SIGUSR2` | Rotate access log file handle (for log rotation tools) |

### 7.3 Windows process-control support

- `CTRL_C_EVENT`, `CTRL_BREAK_EVENT` → graceful shutdown.
- Console close, logoff, and system shutdown events → graceful shutdown.
- Maps to the same shutdown orchestrator as POSIX.

### 7.4 Pre-stop hook (Kubernetes)

Configure in the pod spec to trigger clean shutdown before SIGTERM:

```yaml
lifecycle:
  preStop:
    exec:
      command: ["/bin/sh", "-c", "sleep 5"]
```

Alternatively, set `timeouts.prestop_sleep` and Keel handles the sleep internally on any shutdown signal. This ensures kubelet endpoint removal has propagated before drain begins.

---

## 8. Sidecar behavior: upstream contract

When `sidecar.enabled: true`, Keel acts as a full reverse proxy.

### 8.1 Upstream health probing

Keel periodically probes `upstream_url + upstream_health_path` (default: `/health`). If the upstream returns non-2xx or times out:

1. `/readyz` flips to 503 immediately.
2. Incoming traffic is shed (503).
3. Probing continues; readiness is restored when upstream recovers.

Interval: `sidecar.upstream_health_interval` (default: 10s). Timeout per probe: `sidecar.upstream_health_timeout` (default: 2s).

### 8.2 Circuit breaker

When `sidecar.circuit_breaker.enabled: true`:

- After `failure_threshold` consecutive upstream failures (non-2xx or timeout), the circuit opens.
- While open, requests are rejected immediately with 503 (no upstream call made).
- After `reset_timeout`, the circuit enters half-open: one probe request is allowed through.
- If the probe succeeds, the circuit closes and normal proxying resumes.
- `keel_circuit_open` Prometheus gauge tracks state.

### 8.3 Header forwarding policy

```yaml
sidecar:
  header_policy:
    forward: []   # empty = forward all non-hop-by-hop headers
    strip: []     # always remove these headers before forwarding
```

Hop-by-hop headers (`Connection`, `Transfer-Encoding`, `TE`, `Trailer`, `Upgrade`, `Keep-Alive`, `Proxy-Authorization`, `Proxy-Authenticate`) are always stripped per RFC 7230.

Outbound requests from Keel to upstream include:
- `X-Request-ID` (propagated or generated).
- `Authorization: Bearer <signed-jwt>` if `authn.my_signature_key_file` is set.
- `traceparent` / `tracestate` (OTLP context propagation).

### 8.4 X-Forwarded-For / X-Real-IP policy

```yaml
sidecar:
  xff_mode: append    # append | replace | strip
  xff_trusted_hops: 0 # rightmost N hops to trust
```

| Mode | Behavior |
|---|---|
| `append` | Append client IP to existing `X-Forwarded-For` chain |
| `replace` | Replace `X-Forwarded-For` with client IP (trust no upstream) |
| `strip` | Remove `X-Forwarded-For` entirely |

`X-Real-IP` is set to the trusted client IP (after hop stripping).

### 8.5 Response size cap

`security.max_response_body_bytes` caps the response body read from upstream before forwarding to the client. If the upstream response exceeds the cap, Keel truncates, closes the upstream connection, and returns 502 with an error body. This prevents upstream misbehavior from causing Keel OOM.

### 8.6 Upstream TLS and mTLS (out-of-pod upstreams)

When `upstream_url` uses `https://`, set `sidecar.upstream_tls.enabled: true`. Keel then establishes a TLS connection to the upstream on every proxied request.

**Server certificate verification:**
- By default Keel verifies the upstream's server certificate against the system root CA bundle.
- Set `upstream_tls.ca_file` to a PEM file to pin to a specific CA (e.g., an internal enterprise CA or a self-signed cert used by the upstream).
- `insecure_skip_verify: true` disables verification entirely. **Do not use in production.**

**Mutual TLS (mTLS) — Keel as client:**
- Set `upstream_tls.client_cert_file` and `upstream_tls.client_key_file` to PEM files.
- Keel presents this certificate during the TLS handshake, allowing the upstream to authenticate Keel as a trusted caller.
- These files are typically bind-mounted from a k8s Secret (see §3.8.3).

```yaml
# keel.yaml (out-of-pod upstream with mTLS)
sidecar:
  enabled: true
  upstream_url: https://legacy-api.internal:8443
  upstream_tls:
    enabled: true
    ca_file: /etc/keel/secrets/upstream-ca.crt
    client_cert_file: /etc/keel/secrets/client.crt
    client_key_file: /etc/keel/secrets/client.key
```

```yaml
# keel-secrets.yaml (bind-mounted from k8s Secret)
sidecar:
  upstream_tls:
    ca_file: /etc/keel/secrets/upstream-ca.crt
    client_cert_file: /etc/keel/secrets/client.crt
    client_key_file: /etc/keel/secrets/client.key
```

**Service mesh relationship:** Keel's upstream mTLS is explicit and per-configured-upstream. It is not a replacement for a service mesh. If a service mesh (Istio, Linkerd, Cilium) is present and handles east/west mTLS transparently, `upstream_tls` should be left disabled for intra-cluster connections — the mesh handles it at the network layer. Use `upstream_tls` for:
- Upstreams outside the mesh boundary (VMs, on-prem, third-party APIs).
- Environments with no service mesh deployed.
- Explicit per-upstream trust pinning beyond what the mesh enforces.

---

## 9. Docker Compose test harness

Integration tests run against a named-service Docker Compose topology. This makes port assignments stable and service names DNS-addressable within the test network.

```yaml
# docker-compose.test.yaml
services:

  upstream:
    image: hashicorp/http-echo:latest
    command: ["-text=upstream-ok", "-listen=:3000"]
    networks:
      - keel-test
    ports:
      - "3000:3000"

  keel:
    build:
      context: .
      dockerfile: Dockerfile
    environment:
      KEEL_CONFIG: /etc/keel/keel.yaml
      KEEL_SECRETS: /etc/keel/secrets/keel-secrets.yaml
    volumes:
      - ./tests/fixtures/keel.yaml:/etc/keel/keel.yaml:ro
      - ./tests/fixtures/secrets:/etc/keel/secrets:ro
      - ./tests/fixtures/certs:/etc/keel/tls:ro
    networks:
      - keel-test
    ports:
      - "8080:8080"    # http (main)
      - "8443:8443"    # https (main)
      - "9091:9091"    # /healthz
      - "9092:9092"    # /readyz
      - "9093:9093"    # /startupz
      - "9999:9999"    # admin (/version, /metrics, /debug/pprof)
    depends_on:
      - upstream
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:9091/healthz"]
      interval: 2s
      timeout: 1s
      retries: 10

  prometheus:
    image: prom/prometheus:latest
    volumes:
      - ./tests/fixtures/prometheus.yml:/etc/prometheus/prometheus.yml:ro
    networks:
      - keel-test
    ports:
      - "9292:9090"   # prometheus UI (remapped to avoid conflict with keel admin)

  otel-collector:
    image: otel/opentelemetry-collector-contrib:latest
    volumes:
      - ./tests/fixtures/otel-collector.yaml:/etc/otelcol/config.yaml:ro
    networks:
      - keel-test
    ports:
      - "4317:4317"   # OTLP gRPC
      - "4318:4318"   # OTLP HTTP

  jaeger:
    image: jaegertracing/all-in-one:latest
    networks:
      - keel-test
    ports:
      - "16686:16686"  # Jaeger UI
    environment:
      COLLECTOR_OTLP_ENABLED: "true"

networks:
  keel-test:
    driver: bridge
```

Integration tests reference services by name (`keel:8080`, `upstream:3000`) within the test network, or by `127.0.0.1:<port>` from the host.

```sh
# Run integration tests against the compose stack.
./scripts/ci_test.sh integration
# or
docker compose -f docker-compose.test.yaml up -d
go test ./tests/integration/... -v -timeout 60s
docker compose -f docker-compose.test.yaml down
```

---

## 10. Security governance and vulnerability handling

### 10.1 Vulnerability handling (CVE policy)

- Publish **SECURITY.md**:
  - Private reporting channel.
  - Triage + acknowledgment expectations.
  - Supported versions window / LTS policy.
  - Coordinated disclosure policy.
- Maintain **CHANGELOG** with security fixes clearly marked.
- Ship SBOM/provenance in CI and document it.

### 10.2 Release discipline

- Semantic versioning.
- API stability guarantees + deprecation policy.
- CI gates: unit tests, integration tests (Docker Compose), lint, static analysis, binary size checks, Windows shutdown test.

---

## 11. What this is not

- Not a generic L7 proxy buffet.
- Not a "kitchen sink" mesh replacement.
- Not nginx/haproxy.
- Not a full-blown WAF (no DDoS suite, no rate-limiting product, no bot management, no deep packet inspection).

Keel provides a **minimal, security-first subset** of features that are fully app-integrated. Where overload protection is required, Keel uses **memory backpressure** (readiness flip + 503/429 shedding) to prevent OOM and degrade predictably. For perimeter DDoS defense, use a dedicated solution in front of Keel.