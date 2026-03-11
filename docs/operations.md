# Keel Operations Reference

This document covers runtime operational behavior: graceful shutdown lifecycle, platform-specific signal handling, Kubernetes pre-stop hook patterns, sidecar health probing and circuit breaker state machine, hot config reload, and ACME certificate lifecycle.

---

## 1. Why Graceful Shutdown Matters

When a process receives a termination signal, the worst thing it can do is exit immediately. In-flight requests are dropped mid-response. Clients receive TCP RST or connection-reset errors. Kubernetes reports 502s to end users.

Graceful shutdown is the discipline of:
1. Stopping acceptance of new connections (so no new requests start).
2. Waiting for in-flight requests to complete (so already-started requests finish normally).
3. Flushing side effects (logs, metrics, traces) so nothing is lost.
4. Then exiting cleanly.

Keel implements graceful shutdown on all platforms (POSIX, macOS, Windows). The behavior is deterministic, idempotent, and bounded — it will not hang indefinitely waiting for a stuck request.

---

## 2. Graceful Shutdown Lifecycle

### 2.1 Common Behaviors (All Platforms)

The shutdown sequence is the same regardless of how it was triggered:

1. **Pre-stop sleep** (if `timeouts.prestop_sleep > 0`): Sleep for the configured duration before doing anything. This is a Kubernetes-specific workaround — see Section 4 for details.

2. **Stop accepting new connections**: Keel closes all main listeners (HTTP, HTTPS, H3). New connection attempts receive a TCP connection refused. Connections that are already established and in-flight continue.

3. **Drain in-flight requests**: Keel waits for all currently-processing requests to send their responses and close their connection contexts. This drain is bounded by `timeouts.shutdown_drain` (default: 10s). If requests are still in-flight after the drain timeout, they are forcibly terminated.

4. **Flush logs, metrics, and traces**: Keel flushes the access log writer, the Prometheus registry (final scrape opportunity), and the OTLP trace exporter. This ensures that the last few requests before shutdown appear in your observability data.

5. **Close background workers**: Upstream health probers, ACME renewal goroutines, remote log sink connections, and any other background goroutines are stopped cleanly.

6. **Exit**: Exit code 0 on clean shutdown. Non-zero on error (e.g., drain timeout exceeded, failure to close a listener).

7. **Idempotency**: Multiple termination signals during shutdown are ignored. Sending SIGTERM twice will not cause a double-shutdown race. The first signal starts the sequence; subsequent signals are no-ops until the process exits.

### 2.2 Tuning Shutdown

**`timeouts.shutdown_drain`** (default: 10s): Set this to your worst-case request latency. If your slowest legitimate request takes 8 seconds, set this to 15s to give it room. If you set it too low, long-running requests will be forcibly terminated during rolling deploys.

**`timeouts.prestop_sleep`** (default: 0s): See Section 4 (Kubernetes Pre-Stop Hook) for the recommended value and why it exists.

---

## 3. POSIX / macOS Signal Support

| Signal | Behavior |
|---|---|
| `SIGTERM` | Begin graceful shutdown sequence |
| `SIGINT` | Begin graceful shutdown sequence (same as SIGTERM; typically triggered by Ctrl+C in a terminal) |
| `SIGHUP` | Reload config and secrets files from disk. See Section 5 (Hot Reload). |
| `SIGQUIT` | Dump full goroutine stack trace to stderr (useful for diagnosing goroutine leaks or hung requests), then begin graceful shutdown |
| `SIGUSR1` | Log the current active configuration to stderr. Useful for verifying which config values are in effect without restarting. |
| `SIGUSR2` | Rotate the access log file handle. If the access log is being written to a file (not stdout), send this signal after moving the file to cause Keel to open a new file handle. Compatible with logrotate. |

**Sending signals:**
```sh
# Find the Keel PID (if running as a container, use kubectl exec)
PID=$(pgrep keel)

# Graceful shutdown
kill -TERM $PID

# Hot reload
kill -HUP $PID

# Dump goroutine stack (useful when debugging a hung process)
kill -QUIT $PID

# Print current config
kill -USR1 $PID
```

**In Kubernetes:**
```sh
# Send SIGHUP to a running pod to trigger config reload
kubectl exec -it <pod-name> -- kill -HUP 1
# (assuming Keel is PID 1, which it should be in a scratch container)
```

---

## 4. Windows Process-Control Events

On Windows, there are no POSIX signals. Keel handles the Windows equivalent events:

| Windows Event | Behavior |
|---|---|
| `CTRL_C_EVENT` | Graceful shutdown (equivalent to SIGINT) |
| `CTRL_BREAK_EVENT` | Graceful shutdown |
| Console close | Graceful shutdown |
| System logoff | Graceful shutdown |
| System shutdown | Graceful shutdown |

Windows shutdown events give the process a bounded time window to clean up before the OS forcibly terminates it. Keel's graceful shutdown sequence is designed to complete within that window. Set `timeouts.shutdown_drain` accordingly (system shutdown windows vary by OS version, typically 5–20 seconds).

---

## 5. Kubernetes Pre-Stop Hook: Solving the Endpoint Propagation Race

### 5.1 The Problem

When Kubernetes decides to terminate a pod (during a rolling deploy, scale-down, or node drain), it does two things approximately simultaneously:
1. Removes the pod's IP from the Service endpoint list.
2. Sends SIGTERM to the pod's main container.

Step 1 propagates through the cluster — it has to update kube-proxy on every node, update any Ingress controllers, and update any service mesh sidecars. This propagation is not instantaneous. Depending on cluster size and load, it can take 1–10+ seconds.

Step 2 happens immediately.

The result: Keel might stop accepting new connections (after receiving SIGTERM) before all the load balancers have finished removing it from their endpoint lists. Requests that arrive during this window receive a connection refused error — a 502 from the load balancer's perspective.

### 5.2 The Solution: Pre-Stop Sleep

The fix is to not begin shutdown immediately on SIGTERM. Instead, Keel sleeps for a brief period (`timeouts.prestop_sleep`) to give endpoint propagation time to complete. During this sleep, Keel continues accepting and processing requests normally. After the sleep, it begins the normal shutdown sequence.

Set `timeouts.prestop_sleep: 5s` in your config. This eliminates 502s during rolling deploys for most cluster sizes.

**Important:** The sleep is triggered by receiving SIGTERM (i.e., it is part of Keel's built-in shutdown handling). It is different from the Kubernetes `lifecycle.preStop` hook, which runs before SIGTERM is sent. You can use both:

```yaml
# Helm values.yaml
lifecycle:
  preStop:
    exec:
      command: ["/bin/sh", "-c", "sleep 2"]   # 2s at the Kubernetes layer
```

Combined with `timeouts.prestop_sleep: 5s`, the effective pre-drain window is 7s. The Kubernetes `preStop` hook approach has a downside: it adds to the total termination time for every pod restart, even in cases where propagation is fast. Keel's built-in sleep is simpler and sufficient for most use cases.

### 5.3 Recommended Configuration

```yaml
# keel.yaml for Kubernetes deployment
timeouts:
  shutdown_drain: 15s     # Wait up to 15s for in-flight requests to finish
  prestop_sleep: 5s       # Sleep 5s before starting drain (endpoint propagation window)
```

And in your Kubernetes pod spec:
```yaml
spec:
  terminationGracePeriodSeconds: 30   # Must be > shutdown_drain + prestop_sleep
```

`terminationGracePeriodSeconds` is the hard limit Kubernetes imposes after sending SIGTERM. If the process has not exited by then, Kubernetes sends SIGKILL (which cannot be caught or ignored). Set it generously: at minimum `shutdown_drain + prestop_sleep + buffer` seconds.

---

## 6. Sidecar Health Probing

When running in sidecar mode, Keel actively monitors the upstream service's health via periodic HTTP probes.

**Configuration:**
```yaml
sidecar:
  upstream_health_path: /health          # Path to probe on the upstream
  upstream_health_interval: 10s          # How often to probe
  upstream_health_timeout: 2s            # How long to wait for a probe response
```

**What the health prober does:**

Every `upstream_health_interval`, Keel sends an HTTP GET to `<upstream_url><upstream_health_path>`. If the probe returns a 2xx status within `upstream_health_timeout`, the upstream is considered healthy. If the probe times out or returns a non-2xx status, the probe is counted as a failure.

After `circuit_breaker.failure_threshold` consecutive failures, the circuit opens (see Section 7). When the circuit is closed and probes are passing, `keel_upstream_health` gauge = 1. When probes are failing or the circuit is open, `keel_upstream_health` gauge = 0.

The health prober runs independently of request traffic. Even when the circuit is open (no requests being forwarded), the prober continues. This is how Keel detects that the upstream has recovered and transitions from OPEN to HALF-OPEN.

**Impact on `/readyz`:** When the upstream health prober has accumulated enough failures, Keel flips its `/readyz` endpoint to 503. Kubernetes removes the Keel pod from Service endpoints. This is the correct behavior — if Keel cannot reach its upstream, it should not receive traffic.

---

## 7. Circuit Breaker

The circuit breaker protects Keel from being overwhelmed by requests that are guaranteed to fail (because the upstream is down). Without it, every request would hang for `timeouts.write` seconds waiting for an upstream that is not responding, consuming goroutines and memory until Keel itself becomes unhealthy.

### 7.1 State Machine

The circuit breaker has three states:

```
┌─────────────────────────────────────────────────────────────────┐
│                                                                 │
│   CLOSED ──────────────── failure_threshold consecutive ──────→ OPEN
│   (normal)                failures observed                     │
│                                                                 │
│   CLOSED ←─── probe succeeds ──── HALF-OPEN ←── reset_timeout ─┘
│                                       │
│                                       │ probe fails
│                                       ↓
│                                     OPEN (reset_timeout restarts)
└─────────────────────────────────────────────────────────────────┘
```

**CLOSED (normal operation):**
- All requests are forwarded to the upstream.
- Each upstream error increments the failure counter.
- Each upstream success resets the failure counter to zero.
- When `failure_counter >= failure_threshold`, transition to OPEN.

**OPEN (upstream presumed failed):**
- All requests are rejected immediately with `503 Service Unavailable`. No upstream call is made.
- The upstream health prober continues running.
- `/readyz` returns 503.
- After `circuit_breaker.reset_timeout` elapses, transition to HALF-OPEN.

**HALF-OPEN (testing recovery):**
- One probe request is allowed through to the upstream.
- If the probe succeeds: failure counter resets, transition to CLOSED.
- If the probe fails: transition back to OPEN (reset_timeout starts again).

The half-open state is critical. Without it, the circuit could transition directly from OPEN to CLOSED after the timeout — but the upstream might not be fully recovered yet, and a sudden flood of traffic could overwhelm a partially-recovered upstream.

### 7.2 Configuration

```yaml
sidecar:
  circuit_breaker:
    enabled: true
    failure_threshold: 5    # Open circuit after 5 consecutive failures
    reset_timeout: 30s      # Wait 30s before testing recovery
```

**Tuning guidance:**
- `failure_threshold`: lower values react faster to upstream failures but are more sensitive to transient blips. 5 is a reasonable starting point. If your upstream has occasional slow responses that cause timeouts, you might increase this to 10 to avoid tripping on transient slowness.
- `reset_timeout`: 30s is conservative. If your upstream restarts quickly (e.g., a Kubernetes pod that restarts in 10s), you can reduce this. If your upstream restarts slowly (e.g., a legacy service with a 2-minute startup), increase this to avoid futile half-open probes.

### 7.3 Prometheus Signals

```promql
# Is the circuit currently open?
keel_circuit_open == 1

# Rate of circuit-open transitions (how often is the circuit tripping?)
rate(keel_circuit_open[1h])

# Upstream health history
keel_upstream_health
```

Alert if `keel_circuit_open == 1` for more than 60 seconds — that indicates an upstream that is not recovering quickly.

---

## 8. Hot Config Reload

Keel supports reloading most configuration at runtime. Refer to [docs/config-reference.md](config-reference.md) Section 5 for the complete reference on what can and cannot be reloaded, and how the reload mechanism works.

**Quick summary:**
- Trigger: `SIGHUP` signal or `POST http://localhost:9999/admin/reload`
- On success: new config is active immediately
- On failure: old config stays active; error is logged and returned in the reload response
- TLS certificates can be rotated with zero downtime (no dropped connections)
- Listener ports and protocol bindings cannot be changed without restart

**Reload-safe operations in production:**
```sh
# Rotate TLS cert (cert-manager writes new cert to disk, then trigger reload)
kubectl exec -it <pod-name> -- kill -HUP 1

# Or via admin port (if accessible)
curl -X POST http://<pod-ip>:9999/admin/reload
```

---

## 9. Release Process

Releases are triggered by pushing a `v*` tag to `main`. The `release.yml` CI workflow builds artifacts, signs them with cosign keyless signing, uploads them to the GitHub Release, and publishes container images and Helm chart to GHCR.

### 9.1 Release Notes Policy

Every release MUST include human-readable release notes describing what changed and whether users should upgrade. GitHub auto-generates notes from merged PR titles; maintainers should ensure PR titles follow [Conventional Commits](https://www.conventionalcommits.org/) so the generated notes are meaningful.

**Security releases:** Any release that fixes a vulnerability MUST include a `### Security` section in the GitHub Release body explicitly identifying:

- The vulnerability (CVE if assigned, or a brief description)
- Affected versions
- The fix (what was changed)
- Whether users must upgrade immediately or can defer

Example:

```
### Security

- Fixed improper validation of the `X-Forwarded-For` header that could allow
  a client to spoof its remote address when `xff_mode` is set to `trusted`.
  Affected versions: v0.3.0–v0.4.1. All users should upgrade.
  CVE-2025-XXXXX (if assigned).
```

This ensures downstream users and security scanners can determine whether a release is security-relevant without reading the full diff.

### 9.2 Versioning

Keel follows [Semantic Versioning](https://semver.org/):

- **PATCH** (`v1.2.3`): backward-compatible bug fixes, dependency bumps, documentation updates.
- **MINOR** (`v1.3.0`): backward-compatible new features or configuration fields.
- **MAJOR** (`v2.0.0`): breaking changes to the library API, config schema, or CLI flags.

Security fixes may be released as PATCH regardless of scope.

### 9.3 Running a Release

```sh
# 1. Ensure main is clean and all CI passes.
# 2. Tag and push.
git tag v1.2.3
git push origin v1.2.3
# 3. The release.yml workflow fires automatically.
# 4. After the first ever release, make GHCR packages public (one-time):
bash scripts/release/setup-ghcr.sh

---

## 10. ACME Certificate Lifecycle

> **Canonical specification:** [RFC 8555 — Automatic Certificate Management Environment (ACME)](https://www.rfc-editor.org/rfc/rfc8555)
> Keel implements the ACME client role as defined by RFC 8555. All wire-protocol requirements are satisfied via `golang.org/x/crypto/acme`. See §10.6 for a full compliance statement.

Keel's built-in ACME support is designed for **single-instance deployments** (a VPS, a bare-metal host, a container on a static IP). It automates the full Let's Encrypt certificate lifecycle — issuance, persistence, and renewal — without any external tooling.

For multi-instance or Kubernetes fleets, use an external cert manager (e.g. cert-manager) and point `tls.cert_file` / `tls.key_file` at the shared cert. See Section 10.5.

### 10.1 How It Works

```
Startup
  ├─ Load account key from cache_dir/account.pem  (or generate + persist)
  ├─ Load cert from cache_dir/cert.crt + cert.key  (if present)
  │    └─ Serve cached cert immediately — no CA contact needed
  ├─ Register account with Let's Encrypt (no-op if already registered)
  └─ If cached cert is valid for > 30 days: sleep until renewal window
       └─ Otherwise: obtain cert now

Obtain / Renew
  ├─ Authorize each domain via http-01 challenge on port 80
  ├─ Let's Encrypt GETs /.well-known/acme-challenge/<token>
  ├─ Issue cert; persist to cache_dir/cert.crt + cert.key
  └─ Swap into the running TLS listener atomically (no restart needed)

Background loop
  └─ Sleep until 30 days before cert expiry, then renew
```

The http-01 challenge is served on port 80, which must be reachable from the internet. No DNS changes or DNS API credentials are needed.

### 10.2 Minimal Configuration

```yaml
tls:
  acme:
    enabled: true
    domains: [api.example.com]
    email: ops@example.com
    cache_dir: /var/lib/keel/acme
```

**Multiple domains** — Let's Encrypt issues a single cert covering all listed names. The first entry is the Common Name; all entries appear as Subject Alternative Names:

```yaml
tls:
  acme:
    enabled: true
    domains:
      - api.example.com
      - www.example.com
      - example.com
    email: ops@example.com
    cache_dir: /var/lib/keel/acme
```

All listed domains must resolve to this host at issuance time and at each renewal.

### 10.3 Persistence and Restart Safety

`cache_dir` is the key to restart-safe operation. Keel writes three files there:

| File | Contents | Written |
|---|---|---|
| `account.pem` | ECDSA P-256 ACME account private key | Once, on first run |
| `cert.crt` | PEM certificate chain (leaf + intermediates) | After each issuance/renewal |
| `cert.key` | PEM EC private key for the certificate | After each issuance/renewal |

**On every restart**, Keel reads `cert.crt` and `cert.key` before contacting Let's Encrypt. If the cert is still valid with more than 30 days remaining, Keel serves it immediately and waits until the renewal window opens — no ACME handshake, no rate-limit consumption.

**Consequences of a missing or empty `cache_dir`:**
- Keel contacts Let's Encrypt on every startup and issues a new cert each time.
- Let's Encrypt allows at most 5 duplicate certificates per week. Frequent restarts will exhaust this limit and cause issuance failures.
- Always use a persistent directory — a bind mount, a host path, or a persistent volume. Never use a tmpfs or an ephemeral container filesystem.

**Cron / systemd timer pattern** — If Keel is invoked as a short-lived process (e.g. a cron job that runs, handles a batch, and exits), ACME is still correct provided `cache_dir` persists between invocations. Keel will load the existing cert on each invocation, serve it for the duration, and only contact Let's Encrypt when the cert enters its 30-day renewal window.

### 10.4 Staging and Private CAs

Always test against the Let's Encrypt staging environment before using production. The staging CA issues untrusted certificates but has much higher rate limits.

```yaml
tls:
  acme:
    enabled: true
    domains: [api.example.com]
    email: ops@example.com
    cache_dir: /var/lib/keel/acme-staging
    ca_url: https://acme-staging-v02.api.letsencrypt.org/directory
```

Use a separate `cache_dir` for staging and production — the certs are not interchangeable.

For a private or internal CA (e.g. your own ACME server, or [pebble](https://github.com/letsencrypt/pebble) in a lab), add `ca_cert_file` so Keel trusts its TLS certificate:

```yaml
tls:
  acme:
    enabled: true
    domains: [api.internal]
    email: ops@example.com
    cache_dir: /var/lib/keel/acme
    ca_url: https://acme.internal/directory
    ca_cert_file: /etc/keel/internal-ca.pem
```

### 10.5 Fleet Deployments

ACME is scoped to single-instance use. For fleets:

1. Use [cert-manager](https://cert-manager.io) (Kubernetes) or a dedicated cert renewal job to issue and renew the cert.
2. Store the cert in a Kubernetes Secret or a shared volume.
3. Point Keel's `tls.cert_file` and `tls.key_file` at the shared cert path.
4. Send SIGHUP to Keel after renewal to reload the cert without restarting:
   ```sh
   kill -HUP $(pidof keel)
   ```

This avoids Let's Encrypt rate limits, eliminates the distribution problem, and works across any number of instances.

### 10.6 RFC 8555 Compliance Statement

Keel implements the ACME client role defined by [RFC 8555](https://www.rfc-editor.org/rfc/rfc8555). This section documents what is implemented, one intentional deviation, and known limitations.

**Implemented and compliant**

| RFC 8555 requirement | Section | How satisfied |
|---|---|---|
| HTTPS for all ACME requests | §6.1 | Let's Encrypt production URL is HTTPS by default; `ca_url` should be HTTPS for any non-loopback CA |
| JWS request signing (ES256, ECDSA P-256 account key) | §6.2 | `golang.org/x/crypto/acme` |
| `nonce` in every JWS protected header | §6.2, §6.5 | `golang.org/x/crypto/acme` |
| `badNonce` retry using nonce from error response | §6.5 | `golang.org/x/crypto/acme` |
| POST-as-GET for resource fetches | §6.3 | `golang.org/x/crypto/acme` |
| `url` header set to exact request URL | §6.4 | `golang.org/x/crypto/acme` |
| `User-Agent` on all requests | §6.1 | `golang.org/x/crypto/acme` |
| `Content-Type: application/jose+json` on POSTs | §6.2 | `golang.org/x/crypto/acme` |
| Account registration; 409 Conflict = already registered | §7.3 | `manager.go:registerAccount` |
| Account `kid` (URL) in all post-registration requests | §6.2 | `golang.org/x/crypto/acme` |
| Full order/authz state machine (pending → valid → ready) | §7.4 | `manager.go:obtainCert` via WaitOrder / WaitAuthorization |
| http-01 challenge served on port 80 | §8.3 | `server.go`, port hardcoded |
| Challenge path `/.well-known/acme-challenge/<token>` | §8.3 | `manager.go:HTTPHandler` |
| Challenge response body: key authorization (UTF-8) | §8.1, §8.3 | `manager.go:HTTPHandler` |
| Challenge `Content-Type: application/octet-stream` | §8.3 | `manager.go:HTTPHandler` |
| Signal challenge readiness (POST `{}` to challenge URL) | §7.5.1 | `golang.org/x/crypto/acme:Accept` |
| CSR: PKCS#10 DER, identifiers in SANs (not CN-only) | §7.4 | `manager.go:buildCSR` |
| Certificate key pair distinct from account key pair | §7.4 | `manager.go:obtainCert` generates fresh ECDSA P-256 key per issuance |
| ECDSA P-256 for both account key and certificate key | §6.2, §11 | `manager.go:loadOrCreateAccountKey`, `obtainCert` |
| Certificate chain persisted leaf-first | §7.4.2 | `manager.go:storeCert` |
| Post-issuance: cert SANs must cover all configured domains | beyond RFC | `manager.go:validateCert` |
| Post-issuance: cert public key must be ECDSA | Keel TLS policy | `manager.go:validateCert` |

**Intentional deviation**

- **ToS auto-agreement** (RFC §7.3 SHOULD NOT): Keel calls `xacme.AcceptTOS`, which automatically agrees to the CA's Terms of Service. The RFC discourages this for interactive clients. For unattended server automation there is no practical alternative — certbot, acme.sh, and every other automated ACME client make the same call. Operators accept the CA's terms by configuring ACME.

**Known limitations**

- **Certificate revocation** (RFC §7.6): Not implemented. If a certificate's private key is compromised, revoke it manually via the CA's web interface or API. File a GitHub issue if revocation support is needed.
- **External Account Binding / EAB** (RFC §7.3.4): Not implemented. CAs that require EAB (some enterprise and private CAs) cannot be used. `ca_url` works only for CAs that allow unauthenticated account registration.
- **`Retry-After` on rate-limit errors** (RFC §6.6): On any cert-obtainment failure (including rate limiting), Keel falls back to a 24-hour retry interval. It does not parse `Retry-After` headers from the CA. Let's Encrypt's production rate limits are: 50 certificates per registered domain per week, 5 duplicate certificates per week.
- **Internationalized domain names** (RFC §7.1.4): Domains must be supplied in ACE (ASCII Compatible Encoding) form (e.g., `xn--nxasmq6b.com`). Unicode domain names are not automatically converted and will be rejected by the CA.
- **`ca_url` HTTPS enforcement** (RFC §6.1 MUST): Keel does not validate that `ca_url` starts with `https://`. In practice, all production CAs use HTTPS. Using an `http://` CA URL outside of loopback (test) environments violates RFC 8555 and exposes account keys to interception.
```