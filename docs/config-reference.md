# Keel Configuration Reference

This document is the authoritative reference for every configuration knob in Keel. It covers the YAML schema, environment variable overrides, the secrets file pattern, validation rules, and hot-reload behavior.

If you are new to Keel, read this document alongside the [README](../README.md) to understand how the pieces connect.

---

## How Configuration Works: The Merge Order

Keel loads configuration in **four layers**, each one able to override the previous:

```
Layer 1: Built-in defaults (compiled in)
    ↓ overridden by
Layer 2: keel.yaml (primary config file, path from --config or KEEL_CONFIG)
    ↓ overridden by
Layer 3: keel-secrets.yaml (secrets file, path from --secrets or KEEL_SECRETS)
    ↓ overridden by
Layer 4: KEEL_* environment variables
```

**Why this design?**

This layered approach is the standard "twelve-factor app" pattern for configuration. The YAML file captures the structural configuration that you want to version-control (ports, timeouts, feature flags). The secrets file captures sensitive material (TLS keys, signing keys) that you deliver via Kubernetes Secrets or Vault, never checked into source control. Environment variables let you override any value at deploy time without rebuilding an image — critical for CI/CD pipelines.

The library unmarshals YAML onto a struct that is pre-populated with defaults, so any key you omit in your YAML retains its default value. You only write what you want to change.

---

## 1. YAML Config File

The config file path is set with `--config <path>` or `KEEL_CONFIG=<path>`. If neither is set, Keel starts with only built-in defaults and ENV overrides.

The full schema with every key and its default value:

```yaml
# keel.yaml
#
# This file shows every supported key with its default value.
# You only need to include keys you want to change.

# -----------------------------------------------------------------------
# Listeners: which network ports Keel opens and for what purpose
# -----------------------------------------------------------------------
listeners:
  http:
    enabled: true
    port: 8080        # Plain-text HTTP. Used for ACME challenge, health redirects,
                      # and library mode when TLS is not needed.

  https:
    enabled: false
    port: 8443        # TLS-terminated HTTPS. Requires cert_file+key_file or ACME.

  h3:
    enabled: false
    port: 8443        # HTTP/3 over QUIC (UDP). Shares port number with HTTPS (TLS),
                      # but uses UDP rather than TCP. Requires the same cert as HTTPS.

  health:
    enabled: true
    port: 9091        # Liveness probe endpoint (/healthz). Kubernetes kubelet hits this.

  ready:
    enabled: true
    port: 9092        # Readiness probe endpoint (/readyz). Kubernetes removes pod from
                      # Service endpoints when this returns non-200.

  startup:
    enabled: false
    port: 9093        # Startup probe endpoint (/startupz). Kubernetes uses this during
                      # slow initialization to avoid premature liveness restarts.

  admin:
    enabled: false
    port: 9999        # Administrative port: /version, /debug/pprof, /admin/reload,
                      # /metrics/fips. Never expose this port publicly.

# -----------------------------------------------------------------------
# TLS: certificate and key material for HTTPS and H3 listeners
# -----------------------------------------------------------------------
tls:
  cert_file: ""       # Path to PEM-encoded TLS certificate. Leave empty when using ACME.
  key_file: ""        # Path to PEM-encoded private key. Leave empty when using ACME.

  acme:
    enabled: false    # When true, Keel manages its own certificate via Let's Encrypt.
    domains: []       # List of domain names to certify (SANs). Must not be empty when
                      # ACME is enabled. Example: ["api.example.com", "www.example.com"]
    email: ""         # ACME account email. Used for renewal notifications.
    cache_dir: /var/lib/keel/acme   # Where the certificate files are cached on disk.
                      # Use a PVC or emptyDir in Kubernetes so certs survive restarts.
    ca_url: ""        # ACME CA directory URL. Defaults to Let's Encrypt production when
                      # empty. Override to use Let's Encrypt staging
                      # (https://acme-staging-v02.api.letsencrypt.org/directory) during
                      # development, or an internal CA.

# -----------------------------------------------------------------------
# Sidecar mode: reverse-proxy envelope around an upstream service
# -----------------------------------------------------------------------
sidecar:
  enabled: false

  upstream_url: http://127.0.0.1:3000   # The URL Keel forwards requests to.
                      # For intra-pod (localhost) upstreams, use http:// — TLS is not
                      # needed across the loopback because the pod network namespace is
                      # the trust boundary. For out-of-pod upstreams, use https://.

  upstream_health_path: /health         # Path Keel polls on the upstream to determine
                      # upstream reachability. Used for /readyz and circuit breaker logic.

  upstream_health_interval: 10s         # How often Keel polls the upstream health path.

  upstream_health_timeout: 2s           # How long Keel waits for an upstream health probe
                      # response before counting it as a failure.

  upstream_tls:
    enabled: false    # When true, Keel establishes a TLS (or mTLS) connection to the
                      # upstream. Requires upstream_url to use https://.
    ca_file: ""       # Path to PEM CA bundle used to verify the upstream's certificate.
                      # Leave empty to use the system trust store.
    client_cert_file: ""   # Path to PEM client certificate for mTLS. Leave empty if the
                      # upstream does not require client authentication.
    client_key_file: ""    # Path to PEM client private key for mTLS.
    insecure_skip_verify: false   # NEVER set true in production. Disables upstream cert
                      # verification entirely — only for local development against
                      # self-signed certs.

  circuit_breaker:
    enabled: true
    failure_threshold: 5      # Number of consecutive upstream failures before the circuit
                      # opens and Keel stops forwarding requests.
    reset_timeout: 30s        # Time the circuit stays open before attempting a half-open
                      # probe to check if the upstream has recovered.

  header_policy:
    forward: []       # Headers to explicitly forward to the upstream. Hop-by-hop headers
                      # (Connection, Transfer-Encoding, etc.) are always stripped per
                      # RFC 7230 regardless of this setting.
    strip: []         # Headers to strip before forwarding to the upstream.

  xff_mode: append    # X-Forwarded-For handling mode. Options:
                      #   append  — add client IP to existing XFF header chain (default)
                      #   replace — replace any existing XFF with just the client IP
                      #   strip   — remove XFF entirely (for upstreams that must not see it)

  xff_trusted_hops: 0 # Number of right-most XFF hops to trust as legitimate client IPs.
                      # Use 1 if you have a single trusted load balancer in front of Keel.
                      # 0 means trust nothing — always use the socket peer address.

# -----------------------------------------------------------------------
# Security: OWASP headers and body/header size limits
# -----------------------------------------------------------------------
security:
  owasp_headers: true         # Apply canonical OWASP security headers to every response.
                      # Disable only if your application sets its own headers and you have
                      # verified they meet your security requirements.

  max_header_bytes: 65536     # Maximum aggregate size of all request headers in bytes.
                      # 65536 = 64 KB. Requests with larger headers are rejected 431.
                      # Protects against header-smuggling and memory pressure attacks.

  max_request_body_bytes: 10485760    # Maximum request body size in bytes.
                      # 10485760 = 10 MB. Bodies larger than this are rejected 413.
                      # Protects against upload-based memory exhaustion attacks.

  max_response_body_bytes: 52428800   # Maximum upstream response body size in bytes
                      # (sidecar mode). 52428800 = 50 MB. Upstream responses exceeding
                      # this are truncated and the upstream connection is closed; the
                      # client receives 502.

  hsts_max_age: 63072000      # Strict-Transport-Security max-age in seconds.
                      # 63072000 = 2 years. Applied only on HTTPS responses.
                      # 2 years is the value recommended by the OWASP TLS Cheat Sheet.

# -----------------------------------------------------------------------
# Timeouts: how long Keel waits at each phase of a request
# -----------------------------------------------------------------------
timeouts:
  read_header: 5s     # Maximum time to read request headers from the client. Protects
                      # against slowloris-style attacks that send headers very slowly.

  read: 30s           # Maximum time to read the entire request (headers + body). For
                      # upload-heavy endpoints, consider increasing this.

  write: 30s          # Maximum time to write the full response. Includes proxy time
                      # in sidecar mode — set this higher than your upstream's p99 latency.

  idle: 60s           # Maximum time an idle keep-alive connection can remain open before
                      # Keel closes it. Higher values reduce TCP handshake overhead for
                      # high-request-rate clients.

  shutdown_drain: 10s # Maximum time Keel waits for in-flight requests to complete after
                      # receiving SIGTERM. Requests still in-flight after this window are
                      # forcibly terminated. Set this to your worst-case request latency.

  prestop_sleep: 0s   # Extra sleep before beginning drain. In Kubernetes, there is a race
                      # between a pod being removed from Service endpoints and SIGTERM
                      # arriving. Setting this to 5s eliminates 502s during rolling deploys
                      # by allowing endpoint propagation to complete before Keel stops
                      # accepting new connections. See docs/operations.md for details.

# -----------------------------------------------------------------------
# Limits: concurrency caps and queue depth
# -----------------------------------------------------------------------
limits:
  max_concurrent: 0   # Maximum number of requests allowed in-flight simultaneously.
                      # 0 = unlimited. When the cap is reached, additional requests are
                      # either queued (if queue_depth > 0) or rejected with 429.

  queue_depth: 0      # Maximum number of requests that can queue when max_concurrent is
                      # reached. 0 = no queue (reject immediately). Queued requests that
                      # wait longer than timeouts.write are rejected with 503.

# -----------------------------------------------------------------------
# Backpressure: memory-based load shedding
# -----------------------------------------------------------------------
backpressure:
  heap_max_bytes: 0   # Target heap size limit in bytes. 0 = disable heap-based
                      # backpressure. When set, Keel monitors Go runtime heap usage
                      # and sheds load when pressure rises above high_watermark.

  high_watermark: 0.85    # Fraction of heap_max_bytes at which load shedding begins.
                      # 0.85 = 85%. When heap / heap_max_bytes exceeds this value,
                      # Keel flips /readyz to 503 and starts returning 503 to new
                      # requests. This is the "panic button" — once triggered, Keel
                      # stays in shedding mode until pressure drops below low_watermark.

  low_watermark: 0.70     # Fraction of heap_max_bytes at which load shedding stops.
                      # 0.70 = 70%. The hysteresis gap between high and low watermarks
                      # prevents oscillation — without it, Keel would flap between
                      # shedding and accepting traffic on every GC cycle.

  shedding_enabled: true  # Master switch for load shedding behavior. Set false to
                      # collect pressure metrics without actually shedding traffic
                      # (useful when tuning watermark values).

# -----------------------------------------------------------------------
# Authn: who Keel accepts requests from and who Keel asserts it is
# -----------------------------------------------------------------------
authn:
  enabled: true

  trusted_ids: []     # Allowlist of principal identifiers (stable opaque strings, not
                      # email addresses) whose tokens Keel will accept. Empty list means
                      # any token signed by a trusted signer is accepted.
                      # Example: ["service-a", "service-b", "admin-bot"]

  trusted_signers: [] # List of signing keys, PEM public keys, or JWKs endpoint URLs
                      # that Keel trusts to have issued inbound JWT tokens.
                      # - Bare secret string → HS256 shared secret
                      # - PEM public key     → RS256 or ES256 public key
                      # - https:// URL       → JWKs endpoint (fetched and cached with TTL)

  trusted_signers_file: ""    # Path to a file containing trusted signers, one per line.
                      # Useful when the signer list is long or managed separately.

  my_id: ""           # The principal identifier Keel asserts as its own identity in
                      # outbound JWT tokens (sidecar forwarding mode).

  my_signature_key_file: ""   # Path to private key used to sign outbound JWT tokens.
                      # Keel signs the forwarded request's Authorization header with this
                      # key so the upstream can verify Keel's identity.

# -----------------------------------------------------------------------
# Logging: output format and remote sink
# -----------------------------------------------------------------------
logging:
  json: true          # Emit structured JSON log lines. Set false for human-readable text
                      # output during local development.

  level: info         # Minimum log level. Options: debug, info, warn, error.

  access_log: true    # Emit an access log entry for every HTTP request, including
                      # request ID, trace ID, method, path, status, latency.

  remote_sink:
    enabled: false    # Ship logs to a remote endpoint (e.g., a log aggregator).
    endpoint: ""      # URL of the remote log sink.
    protocol: http    # Protocol used to ship logs. Options: http, grpc.

# -----------------------------------------------------------------------
# Metrics: Prometheus and StatsD output
# -----------------------------------------------------------------------
metrics:
  prometheus: true    # Expose a /metrics endpoint (on the admin port) for Prometheus
                      # scraping. Opt-out at build time with no_prom tag.

  statsd:
    enabled: false    # Emit metrics as StatsD UDP datagrams. Opt-out: no_statsd tag.
    endpoint: ""      # UDP address of the StatsD server. Example: "localhost:8125"
    prefix: keel      # Metric name prefix. All emitted metric names are prefixed with
                      # this value. Example: "keel.requests.total"

# -----------------------------------------------------------------------
# Tracing: OpenTelemetry distributed tracing
# -----------------------------------------------------------------------
tracing:
  otlp:
    enabled: false    # Emit OpenTelemetry spans via OTLP gRPC. Opt-out: no_otel tag.
    endpoint: ""      # OTLP gRPC collector endpoint. Example: "otel-collector:4317"
    insecure: false   # Allow plaintext OTLP connections. Set true for in-cluster
                      # collectors that do not terminate TLS. Never true in prod
                      # if the collector is outside the cluster network.

# -----------------------------------------------------------------------
# FIPS: FIPS mode monitoring
# -----------------------------------------------------------------------
fips:
  monitor: true       # Detect and expose FIPS mode status. When true, Keel checks at
                      # startup whether the running binary was built with boringcrypto
                      # and exposes the result via /health/fips and the keel_fips_active
                      # Prometheus gauge. See docs/FIPS.md for the full FIPS guide.
```

---

## 2. Environment Variable Overrides

Every scalar config value has a corresponding `KEEL_*` environment variable. ENV vars are applied last in the merge order, so they always win.

**Why ENV vars?** They let you change behavior at deploy time — for example, enabling debug logging in staging without modifying the config file — and they work naturally with Kubernetes ConfigMaps, Helm `--set`, and CI/CD systems.

| ENV var | YAML path | Default | Notes |
|---|---|---|---|
| `KEEL_CONFIG` | — | `""` | Path to the primary YAML config file |
| `KEEL_SECRETS` | — | `""` | Path to the secrets YAML file |
| `KEEL_HTTP_ENABLED` | `listeners.http.enabled` | `true` | |
| `KEEL_HTTP_PORT` | `listeners.http.port` | `8080` | |
| `KEEL_HTTPS_ENABLED` | `listeners.https.enabled` | `false` | |
| `KEEL_HTTPS_PORT` | `listeners.https.port` | `8443` | |
| `KEEL_H3_ENABLED` | `listeners.h3.enabled` | `false` | |
| `KEEL_HEALTH_PORT` | `listeners.health.port` | `9091` | |
| `KEEL_READY_PORT` | `listeners.ready.port` | `9092` | |
| `KEEL_STARTUP_PORT` | `listeners.startup.port` | `9093` | |
| `KEEL_ADMIN_PORT` | `listeners.admin.port` | `9999` | |
| `KEEL_TLS_CERT` | `tls.cert_file` | `""` | |
| `KEEL_TLS_KEY` | `tls.key_file` | `""` | |
| `KEEL_SIDECAR` | `sidecar.enabled` | `false` | |
| `KEEL_UPSTREAM_URL` | `sidecar.upstream_url` | `""` | |
| `KEEL_UPSTREAM_TLS` | `sidecar.upstream_tls.enabled` | `false` | |
| `KEEL_UPSTREAM_CA_FILE` | `sidecar.upstream_tls.ca_file` | `""` | |
| `KEEL_UPSTREAM_CLIENT_CERT` | `sidecar.upstream_tls.client_cert_file` | `""` | |
| `KEEL_UPSTREAM_CLIENT_KEY` | `sidecar.upstream_tls.client_key_file` | `""` | |
| `KEEL_OWASP` | `security.owasp_headers` | `true` | |
| `KEEL_AUTHN` | `authn.enabled` | `true` | |
| `KEEL_TRUSTED_IDS` | `authn.trusted_ids` | `""` | Comma-separated list |
| `KEEL_TRUSTED_SIGNERS` | `authn.trusted_signers` | `""` | Comma-separated list |
| `KEEL_MY_ID` | `authn.my_id` | `""` | |
| `KEEL_LOG_JSON` | `logging.json` | `true` | |
| `KEEL_SHEDDING` | `backpressure.shedding_enabled` | `true` | |
| `KEEL_HEAP_MAX_BYTES` | `backpressure.heap_max_bytes` | `0` | |
| `KEEL_MAX_HEADER_BYTES` | `security.max_header_bytes` | `65536` | |
| `KEEL_MAX_REQ_BODY_BYTES` | `security.max_request_body_bytes` | `10485760` | |
| `KEEL_MAX_RESP_BODY_BYTES` | `security.max_response_body_bytes` | `52428800` | |
| `KEEL_READ_HEADER_TIMEOUT` | `timeouts.read_header` | `5s` | Duration string, e.g. `"10s"` |
| `KEEL_READ_TIMEOUT` | `timeouts.read` | `30s` | Duration string |
| `KEEL_WRITE_TIMEOUT` | `timeouts.write` | `30s` | Duration string |
| `KEEL_IDLE_TIMEOUT` | `timeouts.idle` | `60s` | Duration string |
| `KEEL_SHUTDOWN_DRAIN` | `timeouts.shutdown_drain` | `10s` | Duration string |
| `KEEL_PRESTOP_SLEEP` | `timeouts.prestop_sleep` | `0s` | Duration string |
| `KEEL_MAX_CONCURRENT` | `limits.max_concurrent` | `0` | 0 = unlimited |
| `KEEL_QUEUE_DEPTH` | `limits.queue_depth` | `0` | 0 = no queue |

**Duration string format:** All timeout and duration values accept Go duration strings: `"5s"`, `"1m"`, `"500ms"`, `"1h30m"`. Do not use bare integers — they will not parse.

---

## 3. Secrets File

The secrets file is a second YAML file, loaded after the primary config file and before ENV vars. Its values override matching keys from the primary config.

**Why a separate file?** Because the primary config file (`keel.yaml`) is checked into source control. Secrets (TLS private keys, signing keys, API tokens) must never be in source control. The secrets file is delivered at runtime via Kubernetes Secrets, Vault agent injection, or a secrets manager, and is never committed to the repo.

**Kubernetes pattern:** The Helm chart creates a Kubernetes Secret from your `values.yaml` secret values, mounts it as a volume at `/etc/keel/secrets`, and sets `KEEL_SECRETS` to point to `keel-secrets.yaml` within that mount. The pod YAML never contains the raw secret values.

```yaml
# keel-secrets.yaml
# Delivered via Kubernetes Secret bind mount — NEVER committed to source control.

tls:
  cert_file: /etc/keel/tls/tls.crt       # Path to TLS certificate (on the mounted volume)
  key_file: /etc/keel/tls/tls.key         # Path to TLS private key

authn:
  trusted_signers:
    - /etc/keel/secrets/signer-a.pem      # Public key of service A, which Keel will accept
    - /etc/keel/secrets/signer-b.pem      # Public key of service B
  my_signature_key_file: /etc/keel/secrets/my-signing.key   # Keel's own private key
```

**`*_file` key convention:** Any config key that ends in `_file` is treated as a filesystem path. Keel reads the file from disk at startup and again on SIGHUP reload. The file contents are the actual secret value — for example, `my_signature_key_file` contains the raw PEM private key text, not a reference to another path.

**Helm secrets wiring:**
```yaml
# values.yaml (Helm)
secrets:
  existingSecret: my-keel-secrets   # Name of an existing Kubernetes Secret
  mountPath: /etc/keel/secrets      # Where to mount it in the pod
```

---

## 4. Config Validation

Keel validates configuration at startup and **fails fast** with a clear error message if the configuration is invalid. The philosophy is: a misconfigured server that silently starts is more dangerous than one that refuses to start with an actionable error.

Validation checks include:

- **HTTPS/H3 enabled without TLS material:** If `listeners.https.enabled: true` or `listeners.h3.enabled: true`, then either `tls.cert_file` + `tls.key_file` must be set, or `tls.acme.enabled: true` must be set. Otherwise Keel cannot serve HTTPS.

- **ACME + manual cert conflict:** If `tls.acme.enabled: true`, then `tls.cert_file` and `tls.key_file` must be empty. ACME manages the certificate entirely; pointing Keel at a manual cert creates ambiguity about which cert is authoritative.

- **ACME domains empty:** If `tls.acme.enabled: true` but `tls.acme.domains` is empty, Keel cannot request a certificate because it does not know what domain name to certify.

- **Watermark ordering:** `backpressure.high_watermark` must be strictly greater than `backpressure.low_watermark`. If they are equal or inverted, the hysteresis mechanism cannot function — Keel would either never shed load or never recover from shedding.

- **Unreadable secrets file:** If `KEEL_SECRETS` points to a path that does not exist or is not readable, Keel fails at startup rather than starting without secrets.

- **Compiled-out features in config:** If a feature is enabled in config (e.g., `tracing.otlp.enabled: true`) but the binary was built with the corresponding opt-out tag (e.g., `no_otel`), Keel fails at startup with an error indicating the mismatch.

**Dry-run validation:**
```sh
keel --validate --config keel.yaml --secrets keel-secrets.yaml
```

`--validate` checks the configuration without starting any listeners. Exits 0 on success, non-zero with a human-readable error on failure. Use this in CI to catch configuration errors before deploying.

---

## 5. SIGHUP Hot Reload

Keel supports reloading most configuration at runtime without restarting the process or dropping connections. This is useful for rotating TLS certificates, updating signing keys, and adjusting log levels in production.

**How to trigger:**
- Send `SIGHUP` to the Keel process: `kill -HUP <pid>`
- Or POST to the admin port: `curl -X POST http://localhost:9999/admin/reload`

**What happens on reload:**

1. Keel re-reads the YAML config file and secrets file from their original paths.
2. The new config is validated using the same rules as startup validation.
3. If validation fails, Keel logs the error and **keeps running with the old config**. This is intentional — a reload that would break the server is rejected silently so the process stays up.
4. If validation passes, Keel applies changes to the running process.

**What can be reloaded (no restart needed):**
- Log level (`logging.level`)
- Authn signing keys (`authn.trusted_signers`, `authn.trusted_signers_file`)
- Trusted principal IDs (`authn.trusted_ids`)
- Memory backpressure limits (`backpressure.*`)
- Upstream URL (`sidecar.upstream_url`)
- TLS certificate and key (zero-downtime cert rotation — new connections use the new cert; in-flight TLS sessions are not affected)

**What requires a restart (cannot be hot-reloaded):**
- Listener ports (changing `listeners.http.port` etc.)
- Protocol bindings (adding/removing HTTPS, H3, or admin listener)
- Build-tag-controlled features

**`/admin/reload` response:**
- `200 OK` — reload succeeded, new config is active.
- `422 Unprocessable Entity` — reload failed validation; old config is still active. Body contains the validation error.

**Why support reload at all?** TLS certificate rotation is the primary driver. Production certificates expire; ideally you rotate them without any downtime or service interruption. With SIGHUP + cert hot reload, a certificate manager (cert-manager, Vault, acme.sh) can write a new certificate to disk and trigger reload. No pod restart, no dropped connections, no deployment pipeline involvement.