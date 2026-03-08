# HTTP(S) Server Kit (Golang)

[![CI](https://img.shields.io/github/actions/workflow/status/OWNER/REPO/ci.yml?branch=main)](https://github.com/OWNER/REPO/actions)
[![Release](https://img.shields.io/github/v/release/OWNER/REPO)](https://github.com/OWNER/REPO/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/OWNER/REPO.svg)](https://pkg.go.dev/github.com/OWNER/REPO)
[![Go Report Card](https://goreportcard.com/badge/github.com/OWNER/REPO)](https://goreportcard.com/report/github.com/OWNER/REPO)
[![Coverage](https://img.shields.io/codecov/c/github/OWNER/REPO)](https://codecov.io/gh/OWNER/REPO)
[![Security](https://img.shields.io/badge/security-SECURITY.md-blue)](./SECURITY.md)
[![SBOM](https://img.shields.io/badge/SBOM-available-brightgreen)](#security-governance)
[![License](https://img.shields.io/github/license/OWNER/REPO)](./LICENSE)
[![Container](https://img.shields.io/badge/container-5--8MB-success)](#2-stated-objective-10-year-durability)
[![Helm](https://img.shields.io/badge/helm-chart-success)](docs/deployment.md)
[![FIPS](https://img.shields.io/badge/FIPS-compatible-blue)](docs/FIPS.md)

A small-footprint, security-first HTTP(S) core for Kubernetes and long-lived ops. **Not nginx. Not HAProxy.** This is the smallest, most secure subset of features *fully app-integrated*—built to be **durable for 10+ years** and a goal of "30,000 GitHub star" boring.

---

## Documentation

| Document | Contents |
|---|---|
| [docs/config-reference.md](docs/config-reference.md) | Complete YAML schema, ENV vars, secrets file pattern, validation rules, hot reload |
| [docs/security.md](docs/security.md) | OWASP middleware, authn (JWT + mTLS), TLS policy, limits, upstream security |
| [docs/observability.md](docs/observability.md) | Health probes, distributed tracing, Prometheus metrics, StatsD, structured logging, SLO signals |
| [docs/operations.md](docs/operations.md) | Graceful shutdown, signals, Kubernetes pre-stop, circuit breaker, hot reload |
| [docs/deployment.md](docs/deployment.md) | Helm chart (full values reference), Docker Compose test harness, library mode walkthrough |
| [docs/FIPS.md](docs/FIPS.md) | FIPS compliance: BoringCrypto, build instructions, runtime verification, constraints |
| [docs/ROADMAP.md](docs/ROADMAP.md) | Planned future capabilities |
| [SECURITY.md](SECURITY.md) | CVE policy, private reporting, triage timeline, coordinated disclosure |
| [TRADEMARK.md](TRADEMARK.md) | Trademark policy and permitted use |

> **FIPS users:** See [docs/FIPS.md](docs/FIPS.md) for the complete guide to building, running, and verifying FIPS 140 compliance.

---

## 0. Call to Action

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

### 1.1 Size + HTTPS Comparison (Illustrative)

| Option | Typical minimal prod container size | HTTPS/TLS story | Notes |
|---|------------------------------------:|---|---|
| **golang** | **~5-8 MB** | Built-in `crypto/tls`; easy to make TLS1.3-only | Best "small + capable + boring" combo |
| Rust | ~5–15 MB | Strong crates (`rustls`, `hyper/axum`) | Great, but more build complexity |
| Zig | ~10–30+ MB | You bring TCP/TLS/HTTP(S) plumbing | Not "that small" once you add HTTP(S) |
| Python | ~40–120+ MB | Runtime-heavy; TLS via OpenSSL | Great DX; not aligned with "tiny core" |
| Ruby | ~40–120+ MB | Runtime-heavy; TLS via OpenSSL | Same size story as Python |
| Node.js | ~60–150+ MB | Runtime-heavy; TLS via OpenSSL | Often the largest option |

---

## 2. Stated Objective: 10+ Year Durability

This project optimizes for:
- **Longevity**: stable APIs, conservative dependencies, strong upgrade story.
- **Small prod footprint**: core image target **~5–8 MB** (scratch-style + CA certs where needed).
- **Security posture by default**: TLS 1.3-only, safe defaults, documented hardening, proactive vulnerability handling.
- **Operational excellence**: predictable behavior in Kubernetes, systemd, Windows services, and "boring" infra.

---

## 3. Default-On Feature Set with Build-Time Opt-Out

All major features are **built-in by default**. There is **no command-line feature matrix**.

- Runtime config is via YAML file, ENV vars, and secrets file (see [docs/config-reference.md](docs/config-reference.md)).
- **Feature inclusion** is controlled only at build time via negative tags (defaults are **on**, you opt **out**).

### 3.1 CI Discipline: Scripts-First

All CI must use provided **POSIX bash scripts** to the maximum extent possible. CI config files must not contain long chains of inline `run:` lines.

**Rule:** CI invokes comprehensive scripts from `./scripts/` (e.g., `./scripts/ci/build.sh`, `./scripts/test/ci.sh`, `./scripts/release.sh`).

### 3.2 Build-Time Opt-Out Flags

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

**Size rule:** If a feature is opted out, its dependencies are not linked — no container bloat.

### 3.3 Deployment Modes

#### 3.3.1 Library Mode (In-Process)

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

See [docs/deployment.md — Library Mode](docs/deployment.md#1-library-mode-embedding-keel-in-your-go-service) for the complete walkthrough.

#### 3.3.2 Sidecar Mode (Envelope / Reverse-Proxy)

Keel runs as a sidecar and proxies to an upstream service. Two upstream topologies are explicitly supported:

**Intra-pod (localhost upstream):** Keel and the app share a pod. The app listens on `localhost:<port>` over plain HTTP; Keel owns all external-facing ports and applies TLS, authn, OWASP hardening, observability, and backpressure. The pod network namespace is the trust boundary — no TLS is needed on the loopback leg.

**Out-of-pod (remote upstream):** Keel proxies to a service running outside the pod — a legacy VM, a third-party API endpoint, or a service in another namespace not covered by a service mesh. In this topology Keel establishes a TLS or mTLS connection to the upstream, presenting a client certificate if the upstream requires mutual authentication (see [docs/security.md — Upstream TLS and mTLS](docs/security.md#55-upstream-tls-and-mtls)).

**Keel-hauling:** both topologies allow forcing old HTTP/HTTPS services into modern security compliance without rewriting them.

#### 3.3.3 ACME Edge Mode (Standalone TLS Terminator)

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

ACME must not be combined with the `no_acme` build tag. ACME requires `cert_file`/`key_file` to be left empty (Keel manages them).

---

## 4. Authentication and OWASP Hardening (Overview)

Both layers are default-on and independently opt-outable at build time.

**OWASP middleware** (`no_owasp` to opt out) injects canonical security headers on every response (`X-Content-Type-Options`, `X-Frame-Options`, `Content-Security-Policy`, `Strict-Transport-Security`, etc.) and enforces request size and timeout limits. See [docs/security.md — OWASP Middleware](docs/security.md#1-owasp-hardening-middleware) for the full header list with explanations of each one.

**Authn layer** (`no_authn` to opt out) validates incoming JWT bearer tokens (HS256, RS256, ES256) and optionally maps mTLS client certificate identities to principals. `trusted_signers` is the list of keys Keel trusts; `trusted_ids` is the allowlist of principal identifiers. In sidecar mode, Keel re-signs outbound requests as its own identity (`my_id`). See [docs/security.md — Authentication Layer](docs/security.md#2-authentication-layer) for the full trust model.

---

## 5. Extension Model

### 5.1 Route Registration

```go
srv := keel.New(
    keel.WithConfig(cfg),
    keel.WithRegistrar(myapi.NewRegistrar()),
    keel.WithRegistrar(admin.NewRegistrar()),
    keel.WithRoute(ports.HTTP, "/ping", pingHandler),
)
srv.Run(ctx)
```

### 5.2 Built-In Default Route

If no user route claims `port 80` + `/`, Keel serves a built-in default response. This guarantees deterministic "it boots" behavior.

### 5.3 Middleware Export

```go
import "github.com/keelcore/keel/pkg/core/mw"

h := mw.RequestID(
     mw.AccessLog(logger,
     mw.OWASP(cfg,
     yourHandler)))
```

### 5.4 Readiness Dependency Registration

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

### 5.5 Admin Reload Endpoint

`POST /admin/reload` triggers the same hot reload as SIGHUP. Returns 200 on success or 422 if the new config is invalid (old config stays active).

---

## 6. Library Mode Walkthrough

### 6.1 Wrap the Keel Config

```go
import keelconfig "github.com/keelcore/keel/pkg/config"

type AppConfig struct {
    App  AppSettings       `yaml:"app"`
    Keel keelconfig.Config `yaml:"keel"`
}

// Pre-populate with Keel's defaults before unmarshaling your YAML.
// Without this, keys absent from your YAML would get zero values
// rather than Keel's intended defaults.
cfg := AppConfig{Keel: keelconfig.Defaults()}
// ... unmarshal your YAML on top of cfg ...
```

After loading:
```go
keel, err := keelconfig.From(&cfg.Keel)
cfg.Keel = keel
```

### 6.2 Create the Server

```go
import (
    keelcore "github.com/keelcore/keel/pkg/core"
    "github.com/keelcore/keel/pkg/core/logging"
    "github.com/keelcore/keel/pkg/core/ports"
)

log := logging.New(logging.Config{JSON: true})
srv := keelcore.NewServer(log, cfg.Keel)

srv.AddRoute(ports.HTTPS, "GET /hello", http.HandlerFunc(hello))

ctx, cancel := context.WithCancel(context.Background())
defer cancel()

srv.Run(ctx)
```

### 6.3 Your keel.yaml Nests Under the `keel:` Key

```yaml
app:
  name: myapp

keel:
  listeners:
    https: { enabled: true, port: 8443 }
    health: { enabled: true, port: 9091 }
    ready:  { enabled: true, port: 9092 }
  tls:
    cert_file: /etc/myapp/tls.crt
    key_file:  /etc/myapp/tls.key
  authn:
    enabled: true
    my_id: myapp
  logging:
    json: true
    level: info
```

See [docs/deployment.md](docs/deployment.md) for the complete library mode walkthrough, Helm chart reference, and Docker Compose test harness.

---

## Security Governance

- Vulnerability reporting and CVE policy: [SECURITY.md](SECURITY.md)
- SBOM and provenance attached to each GitHub Release.
- FIPS compliance guide: [docs/FIPS.md](docs/FIPS.md)

---

## 11. What This Is Not

- Not a generic L7 proxy buffet.
- Not a "kitchen sink" mesh replacement.
- Not nginx/haproxy.
- Not a full-blown WAF.

Keel provides a **minimal, security-first subset** of features that are fully app-integrated. For perimeter DDoS defense, use a dedicated solution in front of Keel.