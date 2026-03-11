# Keel Observability Reference

This document covers everything Keel exposes to help you understand what it is doing at runtime: health probes, distributed tracing, Prometheus metrics, StatsD, structured logging, operational endpoints, readiness dependency registration, and SLO signal patterns.

If you are new to these concepts, the bridging explanations throughout explain not just what each feature does but why it is designed the way it is.

---

## Why Observability Is First-Class

A service that cannot be observed cannot be operated. Keel treats observability as a core requirement, not an add-on, because:

- **Kubernetes requires probes.** Without correct health endpoints, Kubernetes cannot know when to restart a crashed pod or when to stop sending traffic to a pod that is initializing.
- **Distributed tracing is table stakes.** In a microservice architecture, a request may pass through five services before completing. Without trace context propagation, a slow request is nearly impossible to debug.
- **Prometheus metrics are how SREs sleep at night.** Without the right gauges and counters, you cannot write alerts, cannot create dashboards, and cannot know if your service is meeting its error rate or latency SLOs.
- **Structured logging makes grep useful.** JSON log lines can be queried with Loki, Splunk, Elasticsearch, or any log aggregator. Plain text log lines cannot be reliably parsed at scale.

---

## 1. Kubernetes Health Endpoints

Keel exposes three separate health endpoints, on three separate ports, for the three different Kubernetes probe types.

**Why separate ports?** Kubernetes health probes originate from the kubelet (the node agent), not from the pod's service endpoints. If health endpoints shared a port with main traffic, a misconfigured NetworkPolicy could block kubelet probes and cause Kubernetes to incorrectly mark healthy pods as failed. Separate ports also let you apply different authentication rules — main traffic might require a JWT token; health endpoints should always be unauthenticated.

| Endpoint | Port | Config key | Purpose |
|---|---|---|---|
| `GET /healthz` | `9091` | `listeners.health` | Liveness: is the process alive? |
| `GET /readyz` | `9092` | `listeners.ready` | Readiness: is the process ready for traffic? |
| `GET /startupz` | `9093` | `listeners.startup` | Startup: has the process finished initializing? |

### 1.1 Liveness (`/healthz`)

Returns `200 OK` whenever the process is running and its event loop is healthy. Returns non-200 only if the process is in an unrecoverable state.

**What Kubernetes does with this:** If liveness returns non-200, Kubernetes kills the pod and restarts it. The liveness probe is the "are you dead?" check. It should almost never fail for a healthy process — do not put business logic checks in the liveness probe. A liveness probe that fails too aggressively will cause Kubernetes to restart healthy pods.

### 1.2 Readiness (`/readyz`)

Returns `200 OK` when Keel is ready to serve traffic. Returns `503 Service Unavailable` when:
- Keel is still initializing (before first ready state).
- Memory backpressure has triggered load shedding (`backpressure.shedding_enabled: true`).
- Upstream is unreachable (sidecar mode) — Keel has observed enough failures to flip the circuit.
- Any registered readiness check returns an error (see Section 7 below).

**What Kubernetes does with this:** When readiness returns non-200, Kubernetes removes the pod from the Service's endpoint list. Traffic stops routing to that pod. When readiness returns 200 again, the pod is re-added. The readiness probe is the "are you ready for work?" check — it is appropriate to put dependency checks here (database connectivity, cache availability).

In sidecar mode, `/readyz` also checks upstream reachability. If the upstream health probe (`sidecar.upstream_health_path`) fails `failure_threshold` times, `/readyz` returns 503.

### 1.3 Startup (`/startupz`)

Returns `200 OK` once the process has completed initialization. Returns `503` until then.

**What Kubernetes does with this:** During the startup probe window, Kubernetes does not run the liveness probe. This prevents Kubernetes from killing a legitimately slow-starting pod (e.g., one that runs database migrations at startup) before it has had a chance to finish initializing. The startup probe is the "are you done starting?" check. Once it returns 200, Kubernetes starts running the liveness probe.

**Example Kubernetes probe configuration:**
```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: 9091
  initialDelaySeconds: 5
  periodSeconds: 10
  failureThreshold: 3

readinessProbe:
  httpGet:
    path: /readyz
    port: 9092
  initialDelaySeconds: 5
  periodSeconds: 5
  failureThreshold: 3

startupProbe:
  httpGet:
    path: /startupz
    port: 9093
  failureThreshold: 30      # Allow up to 5 minutes (30 * 10s) for startup
  periodSeconds: 10
```

---

## 2. Distributed Tracing (OpenTelemetry / OTLP)

**Build-time opt-out:** `no_otel`
**Config key:** `tracing.otlp.enabled: true`

### 2.1 What Distributed Tracing Is

In a microservice architecture, a single user request might flow through many services: API gateway → auth service → business logic service → database proxy → cache. If the overall request takes 3 seconds, which service is responsible for the slowdown?

Distributed tracing answers this question. Each service creates a "span" — a record of the time it spent on a particular operation — and links it to the parent span from the service that called it. The result is a tree of spans, called a "trace," that shows the full call path and timing for every step.

A trace has a globally unique `trace_id`. Every span within the trace shares that `trace_id` and has its own `span_id`. Parent-child relationships between spans are recorded via `parent_span_id`.

### 2.2 Trace Context Propagation

Keel propagates trace context using the W3C Trace Context standard (`traceparent` header). This runs unconditionally — it does not require `no_otel` to be absent and has no config toggle.

**On every inbound request:**
1. If a valid `traceparent` header is present, Keel extracts the `trace_id` and preserves it for the lifetime of the request.
2. Keel generates a fresh `span_id` for this hop (16 hex chars, random).
3. Keel sets a `traceparent` response header: `00-<trace_id>-<new_span_id>-01`, so callers can continue the trace on their side.
4. Both values are stored in the request context (see §2.5).

If no inbound `traceparent` is present, both `trace_id` and `span_id` are freshly generated.

**In sidecar mode:** Keel forwards the `traceparent` header to the upstream so the upstream's spans join the same trace, giving end-to-end visibility across the proxy boundary.

**In library mode:** Your application code can access the current span via the OpenTelemetry SDK and add its own child spans.

### 2.3 Span Attributes

Keel creates one span per inbound request with the following attributes:

| Attribute | Value |
|---|---|
| `http.method` | GET, POST, etc. |
| `http.route` | Matched route pattern (e.g., `/api/v1/users/{id}`) |
| `http.status_code` | Response status code |
| `http.request_content_length` | Size of the request body in bytes |
| `upstream.latency_ms` | Time spent waiting for the upstream (sidecar mode only) |

These are the minimum required by the OpenTelemetry HTTP semantic conventions.

### 2.4 Configuration

```yaml
tracing:
  otlp:
    enabled: true
    endpoint: "otel-collector:4317"   # OTLP gRPC collector endpoint
    insecure: true                    # true = plaintext; false = TLS (default false)
```

`insecure: true` is appropriate for in-cluster collectors on the same namespace that do not terminate TLS. For collectors accessible over the internet or outside the cluster network, use `insecure: false` and configure the collector with a TLS certificate.

### 2.5 Context Keys in Library Mode

When using Keel as a library, you can retrieve the trace ID and request ID from the request context:

```go
import "github.com/keelcore/keel/pkg/core/ctxkeys"

requestID := r.Context().Value(ctxkeys.RequestID).(string)
traceID   := r.Context().Value(ctxkeys.TraceID).(string)
spanID    := r.Context().Value(ctxkeys.SpanID).(string)
```

These values are injected by Keel's middleware pipeline before your handler runs.

---

## 3. Prometheus Metrics

**Build-time opt-out:** `no_prom`
**Config key:** `metrics.prometheus: true`
**Endpoint:** `GET /metrics` on the admin port (`9999`)

### 3.1 What Prometheus Metrics Are

Prometheus is a time-series database and monitoring system. Your service exposes a `/metrics` endpoint that Prometheus scrapes on a regular interval (typically every 15 or 30 seconds). The endpoint returns a plain-text format that Prometheus parses and stores.

**Metric types:**
- **Counter:** A monotonically increasing number that only goes up (or resets to zero on process restart). Used for things like "total requests processed." You query counters as rates — `rate(keel_requests_total[5m])` = requests per second over the last 5 minutes.
- **Gauge:** A number that can go up and down. Used for current state — "current in-flight requests," "current heap pressure."
- **Histogram:** A distribution of observations, bucketed by value. Used for latencies and sizes. Allows you to query percentiles — "what is the 99th percentile request duration?"

### 3.2 Keel Metrics Reference

| Metric | Type | Labels | Description |
|---|---|---|---|
| `keel_requests_total` | Counter | `method`, `route`, `status_code` | Total requests received, broken down by HTTP method, matched route, and response status code |
| `keel_request_duration_seconds` | Histogram | `method`, `route`, `status_code` | End-to-end request latency from first byte received to last byte sent |
| `keel_requests_inflight` | Gauge | — | Current number of requests actively being processed |
| `keel_requests_shed_total` | Counter | `reason` | Requests rejected due to memory backpressure or concurrency cap, labeled by reason |
| `keel_memory_pressure` | Gauge | — | Current heap pressure ratio: `current_heap_bytes / heap_max_bytes`. Range 0.0–1.0 |
| `keel_upstream_health` | Gauge | — | Upstream health status in sidecar mode: 1 = healthy, 0 = unhealthy |
| `keel_circuit_open` | Gauge | — | Circuit breaker state in sidecar mode: 1 = open (failing), 0 = closed (normal) |
| `keel_tls_cert_expiry_seconds` | Gauge | — | Seconds until the current TLS certificate expires. Alert when this falls below 30 days (2592000 seconds) |
| `keel_fips_active` | Gauge | — | 1 if the binary is running with FIPS-validated cryptography; 0 otherwise |

### 3.3 Recommended Alert Rules

```yaml
# Prometheus alert rules for Keel

groups:
  - name: keel
    rules:
      # Error rate above 1% over 5 minutes
      - alert: KeelHighErrorRate
        expr: |
          rate(keel_requests_total{status_code=~"5.."}[5m])
          / rate(keel_requests_total[5m]) > 0.01
        for: 5m
        labels:
          severity: warning

      # p99 latency above 500ms over 5 minutes
      - alert: KeelHighLatency
        expr: |
          histogram_quantile(0.99, rate(keel_request_duration_seconds_bucket[5m])) > 0.5
        for: 5m
        labels:
          severity: warning

      # Memory pressure above 80% for more than 2 minutes
      - alert: KeelMemoryPressure
        expr: keel_memory_pressure > 0.80
        for: 2m
        labels:
          severity: warning

      # TLS certificate expiring in less than 30 days
      - alert: KeelCertExpiringSoon
        expr: keel_tls_cert_expiry_seconds < 2592000
        for: 1h
        labels:
          severity: warning

      # Upstream unhealthy for more than 1 minute
      - alert: KeelUpstreamUnhealthy
        expr: keel_upstream_health == 0
        for: 1m
        labels:
          severity: critical

      # Circuit breaker open
      - alert: KeelCircuitOpen
        expr: keel_circuit_open == 1
        for: 1m
        labels:
          severity: critical
```

### 3.4 Prometheus ServiceMonitor (Kubernetes)

When running in Kubernetes with the Prometheus Operator, configure a ServiceMonitor to tell Prometheus how to scrape Keel:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: keel
  labels:
    release: prometheus   # Must match your Prometheus Operator's selector
spec:
  selector:
    matchLabels:
      app: keel
  endpoints:
    - port: admin
      path: /metrics
      interval: 30s
```

See [docs/deployment.md](deployment.md) for the full Helm chart values that wire this up automatically.

---

## 4. StatsD Output

**Build-time opt-out:** `no_statsd`
**Config keys:** `metrics.statsd.enabled`, `metrics.statsd.endpoint`, `metrics.statsd.prefix`

Keel can emit counters, gauges, and timers to a StatsD server over UDP. This allows integration with systems that predate Prometheus (Graphite, Datadog, InfluxDB via Telegraf).

All metric names are prefixed with `metrics.statsd.prefix` (default: `keel`). For example, `keel.requests.total`.

**Configuration:**
```yaml
metrics:
  statsd:
    enabled: true
    endpoint: "localhost:8125"   # UDP address of your StatsD server
    prefix: keel
```

Keel emits in **DogStatsD format** — tags are appended as `|#key:value,key:value`. This is compatible with Datadog, InfluxDB (via Telegraf), and any StatsD server that understands the DogStatsD extension. Tag keys within each metric are sorted alphabetically for deterministic output.

StatsD is a "fire and forget" UDP protocol — Keel does not wait for acknowledgement. If the StatsD server is unavailable, metric datagrams are silently dropped. This is intentional: observability should never cause the primary service to fail.

---

## 5. Structured Logging

**Config keys:** `logging.json`, `logging.level`, `logging.access_log`, `logging.remote_sink`

### 5.1 Why JSON Logging

JSON log lines can be indexed, queried, and aggregated by any log management system (Loki, Elasticsearch/OpenSearch, Splunk, CloudWatch Logs Insights). Plain text log lines require fragile regex parsers. With JSON, you can run queries like `{app="keel"} | json | status >= 400` in Loki and get all error responses instantly.

Set `logging.json: false` during local development if you prefer human-readable output.

### 5.2 Access Log Format

Every inbound HTTP request produces one access log entry:

```json
{
  "ts": "2026-01-01T00:00:00.123Z",
  "level": "info",
  "msg": "access",
  "request_id": "01JXYZ...",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "span_id": "00f067aa0ba902b7",
  "method": "GET",
  "path": "/api/v1/items",
  "query": "page=2&limit=25",
  "status": 200,
  "bytes_in": 0,
  "bytes_out": 512,
  "duration_ms": 4.2,
  "upstream_duration_ms": 3.1,
  "client_ip": "10.0.0.1",
  "user_agent": "keel-client/1.0",
  "principal": "service-a"
}
```

| Field | Description |
|---|---|
| `ts` | RFC3339 timestamp with millisecond precision (UTC) |
| `level` | Always `info` for access log entries |
| `msg` | Always `access` for access log entries |
| `request_id` | Unique ID for this request (ULID — 26-character Crockford base32, timestamp-prefixed). Also sent to client as `X-Request-ID` response header |
| `trace_id` | W3C trace ID from `traceparent` header (or generated if none) |
| `span_id` | W3C span ID for this request's span |
| `method` | HTTP method |
| `path` | Request path (not including query string) |
| `query` | Query string (may be omitted in some configurations for privacy) |
| `status` | HTTP response status code |
| `bytes_in` | Request body size in bytes |
| `bytes_out` | Response body size in bytes |
| `duration_ms` | End-to-end request duration in milliseconds |
| `upstream_duration_ms` | Time spent waiting for upstream (sidecar mode only) |
| `client_ip` | Real client IP (accounting for XFF trusted hops) |
| `user_agent` | Client User-Agent string |
| `principal` | Authenticated principal ID from JWT `sub` claim (if authn is enabled) |

### 5.3 Log Levels

| Level | When to use |
|---|---|
| `debug` | Very verbose. Includes middleware decision details, config parsing steps. Only for active debugging — never in production. |
| `info` | Normal operation. Access logs, startup/shutdown messages, reload events. This is the production default. |
| `warn` | Unexpected but non-fatal events. Upstream health probe failures before circuit trips, config reload rejections. |
| `error` | Failures requiring operator attention. Failed to bind listener, cannot read secrets file. |

Log level can be changed at runtime via SIGHUP reload or `POST /admin/reload` without restarting.

### 5.4 Early-Boot Logging Lifecycle

Keel has a chicken-and-egg problem that every process with a configurable logger
must solve: the logging configuration lives in the config file, but loading and
parsing the config file itself requires a working logger to report errors.

The Linux kernel faces the same issue. The kernel emits early messages through a
hard-wired serial port (`earlycon` / `printk`) long before the driver stack is
initialised. Once the full console driver is up the kernel switches over, but
nothing written before that switch is lost.

Keel follows the same pattern:

**Phase 1 — bootstrap (earlycon equivalent)**

`main()` creates a logger unconditionally before touching any config:

```go
log := logging.New(logging.Config{JSON: true})
```

This writes JSON at `info` level to stdout. It cannot fail. All CLI flag parsing,
config file location resolution, and config load errors flow through this logger.
If the config file is missing or invalid, the error is always visible on stdout.

**Phase 2 — reconfigure (driver handoff)**

Once the config is loaded and validated, `Server.Run` calls
`Logger.Reconfigure` with the user's level and JSON flag — and, if a remote
sink is configured, replaces the output writer with
`io.MultiWriter(stdout, remoteSink)`. From this point forward the operator's
settings are in effect.

Logs emitted during Phase 1 are never lost: they went to stdout before
reconfiguration, and stdout remains part of the tee'd writer afterwards.

**Phase 3 — live reload (SIGHUP / POST /admin/reload)**

On reload, `Reconfigure` is called again with the new config. Level and JSON
flag change atomically. If `logging.remote_sink.*` changed, the old sink
goroutine is cancelled and a new one is started; the logger's output writer is
replaced with a fresh `io.MultiWriter(stdout, newSink)`. If remote sink config
is unchanged or removed, only the level/JSON are updated and the writer is
preserved by setting `cfg.Out = nil` to signal Reconfigure.

**Key guarantees**

| Situation | Behaviour |
|---|---|
| Config file missing at startup | Error logged to stdout via bootstrap logger; process exits 1 |
| `logging.level` invalid in config | Warning logged; previous level preserved |
| Remote sink unreachable at startup | Warning logged; stdout-only logging continues |
| SIGHUP with changed `logging.level` | Level changes in-flight without restarting listeners |
| SIGHUP with changed `logging.json` | JSON flag changes in-flight |
| SIGHUP with changed `remote_sink.*` | Old sink goroutine cancelled; new sink started; writer replaced |

### 5.5 Remote Log Sink

```yaml
logging:
  remote_sink:
    enabled: true
    endpoint: "logs.example.com:514"
    protocol: http    # "http" (default) or "syslog"
```

**Build-time opt-out:** `no_remotelog`

When enabled, Keel ships log entries to the specified endpoint. This is for
environments that do not run a log aggregator as a sidecar (e.g., those that
prefer push-based log ingestion over a DaemonSet-based approach).

**`protocol: http`** (default): log entries are buffered in memory (capacity:
1000 entries) and flushed to the endpoint via HTTP POST with a 5-second
per-flush timeout. When the buffer is full, the oldest entries are dropped to
make room for new ones — log entries are never allowed to block request
handling. On clean shutdown, Keel flushes the buffer before exiting. The
`keel_log_drops_total` metric tracks cumulative dropped entries.

**`protocol: syslog`**: Keel dials `endpoint` over TCP and emits RFC 5424
syslog messages. Suitable for syslog-ng, rsyslog, or any RFC 5424-compatible
aggregator. There is no in-process buffer — if the TCP connection drops, log
lines are lost until reconnection.

Log output is tee'd to stdout regardless of which protocol is configured.

---

## 6. Operational Endpoints (Admin Port)

The admin port (`9999`) provides operational endpoints for operators. Never expose this port publicly — restrict it to internal network access or protect it with a NetworkPolicy.

| Endpoint | Method | Description |
|---|---|---|
| `/version` | GET | Returns JSON with binary version, build tags, FIPS mode status, Go version, and process start time |
| `/health/fips` | GET | Returns `{"fips_active": true}` or `{"fips_active": false}`. Useful for verifying FIPS build compliance |
| `/metrics` | GET | Prometheus metrics endpoint (all Keel metrics) |
| `/debug/pprof/` | GET | Go pprof profiling endpoints: `/debug/pprof/goroutine`, `/debug/pprof/heap`, `/debug/pprof/profile`, etc. |
| `/admin/reload` | POST | Trigger a config + secrets reload (same effect as SIGHUP). Returns 200 on success, 422 with error body on validation failure |

**Example `/version` response:**
```json
{
  "version": "1.2.3",
  "build_tags": ["no_h3"],
  "fips_active": false,
  "go_version": "go1.25.0",
  "start_time": "2026-01-01T00:00:00Z",
  "uptime_seconds": 86400
}
```

**Using pprof:** To take a 30-second CPU profile:
```sh
go tool pprof http://localhost:9999/debug/pprof/profile?seconds=30
```

To take a heap snapshot:
```sh
go tool pprof http://localhost:9999/debug/pprof/heap
```

Pprof is invaluable for diagnosing memory leaks and CPU bottlenecks. It should only be accessible on the internal admin port — the pprof data can reveal sensitive information about your application's internals.

---

## 7. Readiness Dependency Registration

Keel allows you to register custom readiness checks — functions that Keel calls to determine whether your application's dependencies are available. These checks feed into `/readyz` and the memory backpressure system.

**Why register checks?** If your service depends on a database and the database is unavailable, your service cannot serve requests — it should report not-ready so Kubernetes removes it from the load balancer pool. Rather than building this logic into your application, you register a check with Keel and Keel handles the `/readyz` response automatically.

```go
srv := keel.New(
    keel.WithConfig(cfg),

    // Register a readiness check for the database
    keel.WithReadinessCheck("db", func(ctx context.Context) error {
        // This function is called periodically by Keel.
        // Return nil if the dependency is available.
        // Return a non-nil error if it is not.
        return db.PingContext(ctx)
    }),

    // Register a readiness check for the cache
    keel.WithReadinessCheck("cache", func(ctx context.Context) error {
        return cache.Ping(ctx)
    }),
)
```

**Check behavior:**
- Checks are called in parallel to minimize the impact on `/readyz` response time.
- If any check returns an error, `/readyz` returns 503 with a JSON body indicating which checks failed.
- Checks have an implicit timeout — a check that hangs is treated as failing.

**Example `/readyz` failure response:**
```json
{
  "status": "not ready",
  "checks": {
    "db": "ok",
    "cache": "dial tcp 10.0.0.5:6379: connection refused"
  }
}
```

---

## 8. SLO Signals

Service Level Objectives (SLOs) are target values for reliability metrics (error rate, latency). Keel's metrics are designed to make SLO measurement straightforward.

### 8.1 Error Rate SLO

Error rate = fraction of requests that return a 5xx status code.

```promql
# Error rate over the last 5 minutes
rate(keel_requests_total{status_code=~"5.."}[5m])
/ rate(keel_requests_total[5m])
```

Typical SLO: error rate < 0.1% (0.001).

### 8.2 Latency SLO

Latency SLO is typically expressed as a percentile target: "99% of requests complete in under 500ms."

```promql
# p99 latency over the last 5 minutes
histogram_quantile(0.99, rate(keel_request_duration_seconds_bucket[5m]))

# p50 latency (median)
histogram_quantile(0.50, rate(keel_request_duration_seconds_bucket[5m]))

# p999 latency (the "long tail")
histogram_quantile(0.999, rate(keel_request_duration_seconds_bucket[5m]))
```

### 8.3 Availability SLO

Availability = fraction of time the service is ready to serve traffic.

```promql
# Readiness flip observable via the ready port's response codes,
# or track /readyz 200 vs 503 if you scrape that endpoint.
# Alternatively, use keel_requests_shed_total to see when shedding occurs.

# Shedding rate (proxy for unavailability)
rate(keel_requests_shed_total[5m])
/ rate(keel_requests_total[5m])
```

### 8.4 Multi-Window SLO Alerting

For production SLO alerting, use multi-window burn rate alerting (the Google SRE Workbook pattern):

```promql
# Fast burn: consuming 2% error budget in 1 hour (14.4x burn rate for 99.9% SLO)
(
  rate(keel_requests_total{status_code=~"5.."}[1h])
  / rate(keel_requests_total[1h]) > 14.4 * 0.001
)
and
(
  rate(keel_requests_total{status_code=~"5.."}[5m])
  / rate(keel_requests_total[5m]) > 14.4 * 0.001
)
```

This pattern reduces alert noise (avoids alerting on brief spikes) while still catching sustained burn rate problems quickly.