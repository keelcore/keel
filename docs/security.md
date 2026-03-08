# Keel Security Reference

This document covers Keel's security features in depth: OWASP hardening middleware, the authentication layer, rate and size limits, TLS policy, and sidecar upstream security (headers, XFF, circuit breaker, mTLS).

If you are new to these concepts, the bridging explanations throughout this document explain not just what each feature does, but why it exists and how it protects your service.

---

## 1. OWASP Hardening Middleware

**Build-time opt-out:** `no_owasp`
**Config key:** `security.owasp_headers: true`

The OWASP middleware is applied to every response on the main HTTP/HTTPS ports. It injects a set of security response headers that modern browsers use to block common attacks, and enforces size limits that protect the server from memory exhaustion.

### 1.1 What OWASP Is

The Open Web Application Security Project (OWASP) publishes a set of security guidelines and checklists for web applications, including a specific list of HTTP response headers that, when set correctly, instruct browsers to refuse to do dangerous things with your content. These headers are essentially a policy declaration: "hey browser, here is how you are allowed to use the content I am sending you."

Without these headers, browsers apply permissive defaults that allow attacks like clickjacking (embedding your page in an iframe), MIME-sniffing (treating a text file as executable), and cross-site scripting via permissive content policies.

### 1.2 Security Headers Applied

**`X-Content-Type-Options: nosniff`**

Browsers have a feature called MIME-sniffing where they try to detect the actual type of a file by looking at its contents, even if the server declares a different `Content-Type`. This is useful for browsers (so they can handle mistyped content) but dangerous for security — an attacker could upload an HTML file named `image.jpg`, and a vulnerable browser might execute it as HTML. `nosniff` tells the browser to respect the `Content-Type` header exactly and never sniff.

**`X-Frame-Options: DENY`**

This header prevents your page from being embedded in a `<frame>`, `<iframe>`, or `<object>` element on any other page. Without it, an attacker can load your page invisibly inside their page and trick users into clicking buttons they cannot see — a class of attack called "clickjacking." `DENY` means no page anywhere can frame your content.

**`Referrer-Policy: no-referrer`**

When a user clicks a link on your page and navigates to another site, browsers normally include a `Referer` header that tells the destination site where the user came from (your URL). This leaks information. `no-referrer` tells the browser to omit the `Referer` header entirely when navigating away from your site.

**`Content-Security-Policy: default-src 'none'`**

This is the most powerful header in the set. Content Security Policy (CSP) is a browser mechanism that restricts what resources a page can load — scripts, styles, images, fonts, AJAX requests, etc. `default-src 'none'` is the maximally restrictive policy: by default, the page cannot load anything from anywhere. This is the right default for API services and service-to-service endpoints that do not serve HTML. If you are serving HTML with external resources, you need to configure a custom CSP.

**`Permissions-Policy: geolocation=()`**

The Permissions Policy (formerly Feature Policy) controls which browser APIs the page can use. `geolocation=()` disables access to the geolocation API. The default Keel value is conservative; add to it if your app legitimately needs browser APIs.

**`Strict-Transport-Security: max-age=63072000; includeSubDomains`** (HTTPS only)

HSTS tells browsers that your domain must always be accessed over HTTPS, even if the user types `http://` in the address bar. The `max-age` is how long (in seconds) browsers remember this instruction. 63072000 seconds = 2 years. `includeSubDomains` extends the policy to all subdomains. This header is only sent on HTTPS responses — sending it on HTTP would be meaningless and potentially harmful.

### 1.3 Size and Timeout Limits

All size limits are configurable. The defaults are chosen to be safe for typical API workloads without being so restrictive they interfere with legitimate large uploads.

| Limit | Default | What it protects against |
|---|---|---|
| `max_header_bytes` | 64 KB | Header-based memory exhaustion; HTTP header smuggling with large header blocks |
| `max_request_body_bytes` | 10 MB | Upload-based memory exhaustion; requests designed to OOM the server |
| `max_response_body_bytes` | 50 MB | Upstream responses designed to exhaust the sidecar's memory |
| `timeouts.read_header` | 5s | Slowloris attack (client connects but sends headers 1 byte per second to hold the connection open) |
| `timeouts.read` | 30s | Slow-body attack variant of slowloris |
| `timeouts.write` | 30s | Hung upstream connections holding Keel goroutines open |

**What is Keel NOT:** Keel is not a full WAF (Web Application Firewall). It does not inspect request bodies for injection patterns (SQLi, XSS payloads). It provides memory backpressure and structural limits. For deep inspection, use a dedicated WAF solution in front of Keel.

---

## 2. Authentication Layer

**Build-time opt-out:** `no_authn`
**Config key:** `authn.enabled: true`

### 2.1 Concepts: JWTs and Why They Work This Way

A JSON Web Token (JWT) is a compact, URL-safe way to represent claims — assertions about identity — that can be cryptographically verified. A JWT consists of three parts separated by dots:

1. **Header:** `{"alg": "RS256", "typ": "JWT"}` — which algorithm was used to sign this token.
2. **Payload:** `{"sub": "service-a", "iat": 1700000000, "exp": 1700003600}` — the claims. `sub` is the "subject" (who this token is about), `iat` is when it was issued, `exp` is when it expires.
3. **Signature:** a cryptographic signature over the header and payload, computed with the issuer's private key.

To verify a JWT, you need the issuer's public key. You decode the header and payload (they are just base64url), then verify the signature matches. If the signature is valid and the token has not expired, you know:
- The payload has not been tampered with (any modification would invalidate the signature).
- The token was issued by someone who holds the private key corresponding to the public key you used for verification.

This is why Keel's `trusted_signers` list is a list of public keys — those are the keys Keel uses to verify incoming JWTs.

### 2.2 Supported Mechanisms

#### JWT Bearer Token (primary)

Keel validates the `Authorization: Bearer <token>` header on incoming requests.

Supported algorithms:
- **HS256** — HMAC with SHA-256. Shared secret — the same secret is used to both sign and verify. Simpler to set up, but requires the secret to be known by both the issuer and Keel. Use for same-team service-to-service calls.
- **RS256** — RSA with SHA-256. Asymmetric — issuer has a private key; Keel has only the public key. Safer for cross-team trust because Keel never needs the private key.
- **ES256** — ECDSA with P-256 and SHA-256. Asymmetric like RS256 but with smaller keys and faster signature verification.

`trusted_signers` entries:
```yaml
authn:
  trusted_signers:
    - "my-shared-secret-for-hs256"           # Bare string → HS256 shared secret
    - /path/to/rs256-public-key.pem          # File path → RSA/EC public key loaded from disk
    - https://auth.example.com/.well-known/jwks.json   # JWKs URL → fetched + cached
```

`trusted_ids` is an allowlist of `sub` claim values. Empty means any validly-signed token is accepted. Non-empty means only tokens whose `sub` claim appears in the list are accepted — even if the signature is valid.

```yaml
authn:
  trusted_ids:
    - service-a          # Only accept tokens from service-a or service-b
    - service-b
```

#### mTLS Client Certificate Identity (secondary)

When a client presents a TLS client certificate during the handshake, Keel can map the certificate's Subject CN or Subject Alternative Name to a principal ID. The same `trusted_ids` allowlist applies — the mapped ID must be in the list.

This mechanism is used in environments where services authenticate to each other via client certificates rather than bearer tokens (common in service mesh setups without Istio-style transparent mTLS).

### 2.3 Trust Model: Upstream and Downstream

Keel distinguishes between who it accepts traffic from ("upstream trust") and who it presents itself as when forwarding traffic ("downstream trust").

**Upstream trust (who Keel accepts):**
- `trusted_ids[]` — the list of principal IDs Keel will accept. These should be stable machine identifiers, not email addresses. Email addresses change; service identifiers should be permanent.
- `trusted_signers[]` — the keys Keel trusts to have signed incoming tokens.

**Downstream trust (who Keel claims to be):**
- `my_id` — the principal ID Keel asserts as its own `sub` claim in outbound JWTs.
- `my_signature_key_file` — the private key Keel uses to sign outbound Authorization headers. In sidecar mode, Keel strips the inbound JWT and re-signs the forwarded request as itself, so the upstream sees a freshly-signed token from Keel, not the original client token.

**Why re-sign?** In a chain of services (A → Keel → B), if Keel passed A's token through to B, then B would see a token claiming to be from A. That is fine if B trusts A. But it creates a security problem if A is compromised — a compromised A can make requests to B using its own token, bypassing Keel entirely. If Keel re-signs as itself, B only needs to trust Keel, and Keel is responsible for validating A's identity. This reduces the blast radius of any single compromised service.

### 2.4 Bypass and Exemption Rules

Some paths must be exempt from authentication by design:

- `/.well-known/acme-challenge/` is registered before any authn middleware so ACME certificate renewal can proceed without needing a token.
- Health and readiness endpoints (`/healthz`, `/readyz`, `/startupz`) are on separate ports and are never behind authn middleware.
- Admin endpoints are on the admin port (`9999`) which you control at the network level.

---

## 3. Limits and Guardrails

### 3.1 Memory Backpressure

**Config section:** `backpressure`

Keel monitors Go runtime heap usage and uses a two-watermark system to shed load gracefully before the process runs out of memory and is OOM-killed.

**Why this matters:** An OOM kill is the worst possible failure mode — the process dies instantly, in-flight requests are dropped, and Kubernetes may take 30+ seconds to restart and reschedule the pod. By contrast, if Keel starts shedding load at 85% heap usage, in-flight requests complete normally and new requests receive a 503 they can retry. The service degrades rather than dying.

**State machine:**

```
NORMAL (heap < high_watermark)
    → Accept all requests
    → /readyz returns 200

SHEDDING (heap ≥ high_watermark)
    → /readyz returns 503 (Kubernetes removes pod from endpoints)
    → New requests receive 503 (or 429 if queue is full)
    → In-flight requests continue normally

RECOVERY (heap < low_watermark while SHEDDING)
    → Return to NORMAL
    → /readyz returns 200 again
```

The gap between `high_watermark` (0.85) and `low_watermark` (0.70) is the hysteresis band. Without this gap, Keel would oscillate: shed at 85%, GC runs, drops to 84%, accept again, climb back to 85%, shed again — flapping every few seconds. The 15-point gap ensures that once pressure drops, it stays dropped for a meaningful amount of time before traffic is resumed.

**`keel_memory_pressure` Prometheus gauge** reports the current ratio: `current_heap / heap_max_bytes`. Alert on this metric being above 0.75 consistently — that is a sign you need more memory or the service is processing too much data per request.

### 3.2 Concurrency Cap

**Config keys:** `limits.max_concurrent`, `limits.queue_depth`

When `max_concurrent > 0`, Keel uses a semaphore to limit how many requests are actively being processed simultaneously.

**Flow:**
1. Request arrives → try to acquire semaphore slot.
2. Slot available → proceed with request.
3. Slot not available, `queue_depth > 0` → queue the request. If the queued request waits longer than `timeouts.write` → return 503.
4. Slot not available, `queue_depth == 0` → return 429 immediately.

This prevents a sudden spike of slow requests from consuming all goroutines and memory. The concurrency cap is a hard backstop; tune it based on your service's memory per request and your pod memory limit.

### 3.3 Per-Request Timeouts

See Section 1.3 of this document and the [config reference](config-reference.md) for the full timeout table. Timeouts are the first line of defense against slow-loris attacks and hung upstream connections. They are not optional and should be tuned to realistic values for your workload.

---

## 4. TLS Policy

See also [docs/FIPS.md](FIPS.md) for the FIPS-specific TLS constraints.

### 4.1 TLS 1.3 Only

Keel does not support TLS 1.2. The minimum and only allowed version is TLS 1.3.

**Why?**

TLS 1.2, while still considered "acceptable" in many environments, has a much larger attack surface than TLS 1.3:
- TLS 1.2 supports many negotiable cipher suites, some of which are weak (RC4, 3DES, export-grade ciphers). Even a "good" TLS 1.2 configuration is vulnerable to downgrade attacks if not carefully configured.
- TLS 1.2 requires more round trips to establish a connection.
- TLS 1.3 eliminates all of this: it has a fixed, strong cipher suite list (no negotiation), 0-RTT resumption, and forward secrecy is mandatory rather than optional.

Keel's position is: if your client cannot speak TLS 1.3, that client needs to be updated. Modern TLS 1.3 support has been available in all major runtimes since 2018–2019.

**Cipher suites:** In TLS 1.3, cipher suites are not negotiable — the protocol mandates them. Keel does not expose a cipher suite configuration option because there is nothing to configure.

### 4.2 BoringSSL / BoringCrypto

By default, Keel uses Go's standard `crypto/tls` library. When compiled with the `GOEXPERIMENT=boringcrypto` flag (the `fips` image flavor), Keel uses Google's BoringCrypto, which is a Go interface to BoringSSL — Google's FIPS-validated fork of OpenSSL.

BoringCrypto provides:
- FIPS 140-2/3 validated cryptographic operations.
- The same TLS 1.3-only policy.
- FIPS-approved cipher suites in TLS 1.3 (TLS_AES_128_GCM_SHA256, TLS_AES_256_GCM_SHA384).

See [docs/FIPS.md](FIPS.md) for the complete FIPS guide.

### 4.3 Build-Time Embedded Default Certificate

For scratch-style images that need to start without any filesystem dependencies, Keel supports embedding a default certificate at build time. This certificate is used only when no `cert_file`/`key_file` is configured and ACME is not enabled.

**Intended for:** development and "does it boot" testing. Never use a build-time embedded cert in production — the private key would be in the binary and therefore in the container image, where it is not secret.

**Production certificate delivery options:**
- Kubernetes Secret bind mount (recommended — see [config reference](config-reference.md) secrets section).
- ACME automatic certificate management (see YAML schema `tls.acme.*`).
- Platform-provided cert injection (cert-manager, Vault PKI).

### 4.4 ACME / Let's Encrypt Automatic Certificates

When `tls.acme.enabled: true`, Keel becomes its own certificate manager.

**How ACME http-01 challenge works:**

1. Keel contacts the ACME CA (Let's Encrypt by default) and says "I want a certificate for `api.example.com`."
2. The CA responds with a challenge: "Prove you control `api.example.com` by serving this specific token at `http://api.example.com/.well-known/acme-challenge/<token-id>`."
3. Keel registers that token at that path on its HTTP listener (port 80).
4. The CA's servers fetch `http://api.example.com/.well-known/acme-challenge/<token-id>` and verify the response.
5. Challenge passes → CA issues the certificate.

**Why port 80 must stay open during ACME:** The http-01 challenge requires plaintext HTTP on port 80. Even if Keel redirects all other HTTP traffic to HTTPS, the `/.well-known/acme-challenge/` path must be served over plain HTTP. Keel handles this automatically — it registers the challenge path before any redirect or authn middleware.

**Automatic renewal:** Keel renews the certificate before it expires (typically when 30 days remain). Renewal uses the same challenge mechanism. After renewal, Keel hot-reloads the certificate without dropping connections.

---

## 5. Sidecar Upstream Security

When running in sidecar mode, Keel proxies requests to an upstream service. This section covers how Keel handles headers, IP attribution, response size, and upstream TLS.

### 5.1 Header Forwarding Policy

**Hop-by-hop headers are always stripped.** Per RFC 7230, headers like `Connection`, `Transfer-Encoding`, `TE`, `Upgrade`, and `Keep-Alive` are specific to a single HTTP connection and must not be forwarded to the next hop. Keel strips these automatically regardless of any `header_policy` configuration.

**Headers Keel adds outbound:**
- `X-Request-ID` — the request ID Keel assigned to this request (useful for correlating logs across services).
- `Authorization: Bearer <jwt>` — a fresh JWT signed by Keel's `my_signature_key_file`, asserting Keel's `my_id`. This replaces any inbound Authorization header.
- `traceparent` / `tracestate` — W3C Trace Context propagation headers, so the upstream can join the distributed trace.

**Custom forwarding rules:**
```yaml
sidecar:
  header_policy:
    forward:
      - X-Custom-Header    # Explicitly forward this header to the upstream
    strip:
      - X-Internal-Token   # Strip this header before forwarding
```

### 5.2 X-Forwarded-For Policy

`X-Forwarded-For` (XFF) is a de-facto standard header for conveying the original client IP through a chain of proxies. Each proxy appends the IP of the client it received the request from. So if Client → LB → Keel → App, the App sees `X-Forwarded-For: <client-ip>, <lb-ip>`.

**The XFF trust problem:** If you blindly trust the leftmost IP in XFF as the real client IP, an attacker can forge it by sending `X-Forwarded-For: 127.0.0.1` in their request. XFF is only as trustworthy as the proxy chain you control.

`xff_trusted_hops` tells Keel how many right-most hops in the XFF chain were added by trusted infrastructure (your load balancers). Keel uses this to find the real client IP:

```
XFF: attacker-forged-ip, real-client-ip, trusted-lb-ip
xff_trusted_hops: 1

Real client IP = XFF[-1 - trusted_hops] = XFF[-2] = real-client-ip
```

**XFF modes:**

| Mode | Behavior | Use when |
|---|---|---|
| `append` | Add socket peer IP to existing XFF chain | Standard proxy — you want the upstream to see the full chain |
| `replace` | Replace all of XFF with just the client IP | Upstream only needs the real client IP, not the full chain |
| `strip` | Remove XFF entirely | Upstream must not see any client IP (privacy requirement) |

`X-Real-IP` is always set to the trusted client IP (as determined by `xff_trusted_hops`), regardless of XFF mode.

### 5.3 Response Size Cap

When an upstream response body exceeds `security.max_response_body_bytes` (default 50 MB), Keel:
1. Stops reading the upstream response body.
2. Closes the upstream connection.
3. Returns `502 Bad Gateway` to the client.

**Why truncation returns 502 rather than serving the partial response:** A truncated HTTP body is not valid — the client would receive a partial response with no indication that it was truncated. 502 makes the truncation explicit and retriable.

### 5.4 Circuit Breaker

The circuit breaker prevents Keel from hammering a failing upstream and giving failing requests a chance to queue up.

**State machine:**

```
CLOSED (normal operation)
    → All requests forwarded to upstream
    → Failure counter incremented on each upstream error

    failure_counter >= failure_threshold
    → Transition to OPEN

OPEN (upstream presumed unhealthy)
    → All requests rejected with 503 immediately (no upstream call made)
    → /readyz returns 503

    reset_timeout expires
    → Transition to HALF-OPEN

HALF-OPEN (testing recovery)
    → One probe request allowed through to upstream

    Probe succeeds
    → Reset failure counter, transition to CLOSED

    Probe fails
    → Transition back to OPEN (reset_timeout starts again)
```

**Why a circuit breaker?** Without one, when an upstream is slow or failing, requests pile up, goroutines pile up, memory fills, and Keel itself becomes unhealthy. The circuit breaker short-circuits this: as soon as enough failures are observed, Keel stops making upstream calls and starts returning fast failures. This protects Keel's memory and allows the upstream time to recover without being bombarded.

**Prometheus metrics:**
- `keel_upstream_health` gauge: 1 = upstream healthy, 0 = unhealthy.
- `keel_circuit_open` gauge: 1 = circuit open, 0 = circuit closed.

### 5.5 Upstream TLS and mTLS

When the upstream is outside the pod (on another host, in another namespace, or on the internet), Keel can establish a TLS connection with optional mutual TLS (mTLS).

**Configuration:**
```yaml
sidecar:
  upstream_url: https://legacy-api.internal:8443
  upstream_tls:
    enabled: true
    ca_file: /etc/keel/secrets/upstream-ca.crt    # Custom CA to verify upstream cert
    client_cert_file: /etc/keel/secrets/client.crt # Keel's client cert for mTLS
    client_key_file: /etc/keel/secrets/client.key  # Keel's client private key
```

**`ca_file`:** If the upstream's TLS certificate was issued by a private CA (not a public CA like Let's Encrypt), you need to give Keel that CA's certificate so it can verify the upstream. Without this, Keel cannot establish a TLS connection to a private-CA-signed upstream. Leave empty to use the system trust store (appropriate for publicly-signed certs).

**mTLS:** When `client_cert_file` and `client_key_file` are set, Keel presents its client certificate during the TLS handshake with the upstream. This allows the upstream to verify that the connecting client is Keel, not just any TLS client. Use this when the upstream requires mutual authentication.

**Relationship with service mesh:** If you are running Istio or Linkerd and all service-to-service traffic is transparently mTLS'd by the mesh, you typically do not need `upstream_tls` for in-mesh traffic. Use `upstream_tls` for:
- Upstreams outside the mesh boundary (legacy VMs, third-party APIs).
- No-mesh environments.
- Explicit per-upstream trust pinning that you want independent of the mesh.

**`insecure_skip_verify`:** Never set this to true in production. It disables all upstream certificate verification — Keel will connect to any server claiming to be your upstream, including an attacker performing a man-in-the-middle attack. The only legitimate use is local development against a self-signed cert when you cannot install the CA.