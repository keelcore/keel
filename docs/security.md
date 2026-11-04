# Keel Security Reference

This document covers Keel's security features in depth: OWASP hardening middleware, the authentication layer,
rate and size limits, TLS policy, and sidecar upstream security (headers, XFF, circuit breaker, mTLS).

If you are new to these concepts, the bridging explanations throughout this document explain not just what each
feature does, but why it exists and how it protects your service.

---

## 1. OWASP Hardening Middleware

**Build-time opt-out:** `no_owasp`
**Config key:** `security.owasp_headers: true`

The OWASP middleware is applied to every response on the main HTTP/HTTPS ports. It injects a set of security
response headers that modern browsers use to block common attacks, and enforces size limits that protect the
server from memory exhaustion.

### 1.1 What OWASP Is

The Open Web Application Security Project (OWASP) publishes a set of security guidelines and checklists for
web applications, including a specific list of HTTP response headers that, when set correctly, instruct browsers
to refuse to do dangerous things with your content. These headers are essentially a policy declaration: "hey
browser, here is how you are allowed to use the content I am sending you."

Without these headers, browsers apply permissive defaults that allow attacks like clickjacking (embedding your
page in an iframe), MIME-sniffing (treating a text file as executable), and cross-site scripting via permissive
content policies.

### 1.2 Security Headers Applied

**`X-Content-Type-Options: nosniff`**

Browsers have a feature called MIME-sniffing where they try to detect the actual type of a file by looking at
its contents, even if the server declares a different `Content-Type`. This is useful for browsers (so they can
handle mistyped content) but dangerous for security — an attacker could upload an HTML file named `image.jpg`,
and a vulnerable browser might execute it as HTML. `nosniff` tells the browser to respect the `Content-Type`
header exactly and never sniff.

**`X-Frame-Options: DENY`**

This header prevents your page from being embedded in a `<frame>`, `<iframe>`, or `<object>` element on any
other page. Without it, an attacker can load your page invisibly inside their page and trick users into clicking
buttons they cannot see — a class of attack called "clickjacking." `DENY` means no page anywhere can frame
your content.

**`Referrer-Policy: no-referrer`**

When a user clicks a link on your page and navigates to another site, browsers normally include a `Referer`
header that tells the destination site where the user came from (your URL). This leaks information.
`no-referrer` tells the browser to omit the `Referer` header entirely when navigating away from your site.

**`Content-Security-Policy: default-src 'none'`**

This is the most powerful header in the set. Content Security Policy (CSP) is a browser mechanism that
restricts what resources a page can load — scripts, styles, images, fonts, AJAX requests, etc.
`default-src 'none'` is the maximally restrictive policy: by default, the page cannot load anything from
anywhere. This is the right default for API services and service-to-service endpoints that do not serve HTML.
If you are serving HTML with external resources, you need to configure a custom CSP.

**`Permissions-Policy: geolocation=()`**

The Permissions Policy (formerly Feature Policy) controls which browser APIs the page can use.
`geolocation=()` disables access to the geolocation API. The default Keel value is conservative; add to it if
your app legitimately needs browser APIs.

**`Strict-Transport-Security: max-age=63072000; includeSubDomains`** (HTTPS only)

HSTS tells browsers that your domain must always be accessed over HTTPS, even if the user types `http://` in
the address bar. The `max-age` is how long (in seconds) browsers remember this instruction. 63072000 seconds
= 2 years. `includeSubDomains` extends the policy to all subdomains. This header is only sent on HTTPS
responses — sending it on HTTP would be meaningless and potentially harmful.

### 1.3 Size and Timeout Limits

All size limits are configurable. The defaults are chosen to be safe for typical API workloads without being
so restrictive they interfere with legitimate large uploads.

| Limit | Default | What it protects against |
| --- | --- | --- |
| `max_header_bytes` | 64 KB | Header-based memory exhaustion; HTTP header smuggling with large header blocks |
| `max_request_body_bytes` | 10 MB | Upload-based memory exhaustion; requests designed to OOM the server |
| `max_response_body_bytes` | 50 MB | Upstream responses designed to exhaust the sidecar's memory |
| `timeouts.read_header` | 5s | Slowloris attack (client connects but sends headers 1 byte per second to hold the connection open) |
| `timeouts.read` | 30s | Slow-body attack variant of slowloris |
| `timeouts.write` | 30s | Hung upstream connections holding Keel goroutines open |

**What is Keel NOT:** Keel is not a full WAF (Web Application Firewall). It does not inspect request bodies for
injection patterns (SQLi, XSS payloads). It provides memory backpressure and structural limits. For deep
inspection, use a dedicated WAF solution in front of Keel.

---

## 2. Authentication Layer

**Build-time opt-out:** `no_authn`
**Config key:** `authn.enabled: true`

### 2.1 Concepts: JWTs and Why They Work This Way

A JSON Web Token (JWT) is a compact, URL-safe way to represent claims — assertions about identity — that can
be cryptographically verified. A JWT consists of three parts separated by dots:

1. **Header:** `{"alg": "RS256", "typ": "JWT"}` — which algorithm was used to sign this token.
2. **Payload:** `{"sub": "service-a", "iat": 1700000000, "exp": 1700003600}` — the claims. `sub` is the
   "subject" (who this token is about), `iat` is when it was issued, `exp` is when it expires.
3. **Signature:** a cryptographic signature over the header and payload, computed with the issuer's private key.

To verify a JWT, you need the issuer's public key. You decode the header and payload (they are just base64url),
then verify the signature matches. If the signature is valid and the token has not expired, you know:

- The payload has not been tampered with (any modification would invalidate the signature).
- The token was issued by someone who holds the private key corresponding to the public key you used for verification.

This is why Keel's `trusted_signers` list is a list of public keys — those are the keys Keel uses to verify
incoming JWTs.

### 2.2 Supported Mechanisms

#### JWT Bearer Token (primary)

Keel validates the `Authorization: Bearer <token>` header on incoming requests.

Supported algorithms:

- **HS256** — HMAC with SHA-256. Shared secret — the same secret is used to both sign and verify. Simpler to
  set up, but requires the secret to be known by both the issuer and Keel. Use for same-team
  service-to-service calls.
- **RS256** — RSA with SHA-256. Asymmetric — issuer has a private key; Keel has only the public key. Safer
  for cross-team trust because Keel never needs the private key.
- **ES256** — ECDSA with P-256 and SHA-256. Asymmetric like RS256 but with smaller keys and faster signature
  verification.

`trusted_signers` entries:

```yaml
authn:
  trusted_signers:
    - "my-shared-secret-for-hs256"           # Bare string → HS256 shared secret
    - /path/to/rs256-public-key.pem          # File path → RSA/EC public key loaded from disk
    - https://auth.example.com/.well-known/jwks.json   # JWKs URL → fetched + cached
```

**JWKs URL behavior:** When an entry starts with `https://`, Keel fetches the JWK Set from that URL and
caches it with a **5-minute TTL**. On each request, if the cache is still fresh the fetch is skipped. After
the TTL, a background fetch refreshes the keys.

Key matching within the fetched set:

1. If the incoming JWT has a `kid` (Key ID) header, Keel selects only keys where `kid` matches.
2. If no `kid` is present, Keel tries all keys whose type matches the JWT algorithm.

Supported key types and algorithms from JWKs:

- `kty: RSA` — RS256, RS384, RS512
- `kty: EC` with `crv: P-256` — ES256; with `crv: P-384` — ES384

Keel tries each configured `trusted_signers` entry in order and accepts the first one that validates the
token.

`trusted_ids` is an allowlist of `sub` claim values. Empty means any validly-signed token is accepted.
Non-empty means only tokens whose `sub` claim appears in the list are accepted — even if the signature is
valid.

```yaml
authn:
  trusted_ids:
    - service-a          # Only accept tokens from service-a or service-b
    - service-b
```

#### Inbound mTLS (transport gate)

Set `tls.client_ca_file` (env `KEEL_TLS_CLIENT_CA`; Helm `keel.tls.clientCASecretName`, a Secret with `ca.crt`) and
the HTTPS and H3 listeners require every client to present a certificate that chains to one of those CAs; the handshake
fails otherwise. This is a transport-level gate only: the certificate's Subject or SANs are **not** mapped to a principal
and are not checked against `trusted_ids`. Per-caller identity comes from JWT authn above. Plain HTTP is unaffected.

### 2.3 Trust Model: Upstream and Downstream

Keel distinguishes between who it accepts traffic from ("upstream trust") and who it presents itself as when forwarding
traffic ("downstream trust").

**Upstream trust (who Keel accepts):**

- `trusted_ids[]` — the list of principal IDs Keel will accept. These should be stable machine identifiers,
  not email addresses. Email addresses change; service identifiers should be permanent.
- `trusted_signers[]` — the keys Keel trusts to have signed incoming tokens.

**Downstream trust (who Keel claims to be):**

- `my_id` — the principal ID Keel asserts as its own `sub` claim in outbound JWTs.
- `my_signature_key_file` — the private key Keel uses to sign outbound Authorization headers. In sidecar mode, Keel
  strips the inbound JWT and re-signs the forwarded request as itself, so the upstream sees a freshly-signed token from
  Keel, not the original client token.

**Why re-sign?** In a chain of services (A → Keel → B), if Keel passed A's token through to B, then B would see a
token claiming to be from A. That is fine if B trusts A. But it creates a security problem if A is compromised — a
compromised A can make requests to B using its own token, bypassing Keel entirely. If Keel re-signs as itself, B only
needs to trust Keel, and Keel is responsible for validating A's identity. This reduces the blast radius of any single
compromised service.

### 2.4 Bypass and Exemption Rules

Some paths must be exempt from authentication by design:

- `/.well-known/acme-challenge/` is registered before any authn middleware so ACME certificate renewal can proceed
  without needing a token.
- Health and readiness endpoints (`/healthz`, `/readyz`, `/startupz`) are on separate ports and are never behind authn
  middleware.
- Admin endpoints are on the admin port (`9999`) which you control at the network level.

---

## 3. Rate Limiting, Backpressure, and Guardrails

Keel does not implement a token-bucket per-client rate limiter. Instead it provides two complementary mechanisms that
together achieve the same practical outcome: the concurrency cap enforces an explicit ceiling on simultaneous in-flight
work and returns 429 to callers when that ceiling is reached; the memory backpressure loop monitors heap usage and flips
`/readyz` to 503 when the process is under pressure, causing the load balancer to stop routing new traffic entirely. At
normal load, the concurrency cap absorbs bursts. Under sustained overload or memory pressure, the backpressure system
signals the LB to redistribute traffic to other pods or to trigger autoscaling. Together they protect both the process
and the upstream service without requiring per-client state.

### 3.1 Memory Backpressure

**Config section:** `backpressure`

Keel monitors Go runtime heap usage and uses a two-watermark system to shed load gracefully before the process runs out
of memory and is OOM-killed.

**Why this matters:** An OOM kill is the worst possible failure mode — the process dies instantly, in-flight requests
are dropped, and Kubernetes may take 30+ seconds to restart and reschedule the pod. By contrast, if Keel starts shedding
load at 85% heap usage, in-flight requests complete normally and new requests receive a 503 they can retry. The service
degrades rather than dying.

**State machine:**

```text
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

The gap between `high_watermark` (0.85) and `low_watermark` (0.70) is the hysteresis band. Without this gap, Keel would
oscillate: shed at 85%, GC runs, drops to 84%, accept again, climb back to 85%, shed again — flapping every few seconds.
The 15-point gap ensures that once pressure drops, it stays dropped for a meaningful amount of time before traffic is
resumed.

**`keel_memory_pressure` Prometheus gauge** reports the current ratio: `current_heap / heap_max_bytes`. Alert on this
metric being above 0.75 consistently — that is a sign you need more memory or the service is processing too much data
per request.

### 3.2 Concurrency Cap

**Config keys:** `limits.max_concurrent`, `limits.queue_depth`

When `max_concurrent > 0`, Keel uses a semaphore to limit how many requests are actively being processed simultaneously.

**Flow:**

1. Request arrives → try to acquire semaphore slot.
2. Slot available → proceed with request.
3. Slot not available, `queue_depth > 0` → queue the request. If the queued request waits longer than `timeouts.write`
   → return 503.
4. Slot not available, `queue_depth == 0` → return 429 immediately.

This prevents a sudden spike of slow requests from consuming all goroutines and memory. The concurrency cap is a hard
backstop; tune it based on your service's memory per request and your pod memory limit.

### 3.3 Per-Request Timeouts

See Section 1.3 of this document and the [config reference](config-reference.md) for the full timeout table. Timeouts
are the first line of defense against slow-loris attacks and hung upstream connections. They are not optional and should
be tuned to realistic values for your workload.

---

## 4. TLS Policy

**Build-time opt-out (HTTP/2):** `no_h2`
**Build-time opt-out (HTTP/3):** `no_h3`

See also [docs/FIPS.md](FIPS.md) for the FIPS-specific TLS constraints.

### 4.1 TLS 1.3 Only

Keel does not support TLS 1.2. The minimum and only allowed version is TLS 1.3.

**Why?**

TLS 1.2, while still considered "acceptable" in many environments, has a much larger attack surface than TLS 1.3:

- TLS 1.2 supports many negotiable cipher suites, some of which are weak (RC4, 3DES, export-grade ciphers). Even a
  "good" TLS 1.2 configuration is vulnerable to downgrade attacks if not carefully configured.
- TLS 1.2 requires more round trips to establish a connection.
- TLS 1.3 eliminates all of this: it has a fixed, strong cipher suite list (no negotiation), 0-RTT resumption, and
  forward secrecy is mandatory rather than optional.

Keel's position is: if your client cannot speak TLS 1.3, that client needs to be updated. Modern TLS 1.3 support has
been available in all major runtimes since 2018–2019.

**Cipher suites:** In TLS 1.3, cipher suites are not negotiable — the protocol mandates them. Keel does not expose a
cipher suite configuration option because there is nothing to configure.

### 4.2 Go Cryptographic Module (FIPS 140-3)

By default, Keel uses Go's standard `crypto/tls`. The `fips` flavor (`make max`) is built with the Go Cryptographic
Module pinned to the CMVP-certified version (`GOFIPS140=v1.0.0`, CMVP certificate #5247). It is part of the Go standard
library: pure Go, no cgo, so the FIPS binary stays static and scratch-friendly.

In FIPS 140 mode the module provides:

- FIPS 140-3 validated cryptographic operations.
- The same TLS 1.3-only policy, with key exchange limited to P-256 and P-384.
- Only FIPS-approved TLS 1.3 cipher suites (TLS_AES_128_GCM_SHA256, TLS_AES_256_GCM_SHA384).

See [docs/FIPS.md](FIPS.md) for the complete FIPS guide.

### 4.3 Certificate Delivery

TLS certificates are delivered at runtime — never embedded in the binary. Supported mechanisms:

- **Kubernetes Secret bind mount** (recommended) — mount the cert and key as a Secret volume; set `tls.cert_file` and
  `tls.key_file` to the mounted paths. cert-manager, Vault PKI, or SPIFFE/SPIRE can write and rotate the Secret
  automatically.
- **ACME / Let's Encrypt** — set `tls.acme.enabled: true`; Keel manages the full certificate lifecycle. See §4.4.
- **Docker Compose / local dev** — use `tests/fixtures/gen-certs.sh` to generate self-signed certs;
  bind-mount them into the container.

### 4.4 ACME / Let's Encrypt Automatic Certificates

When `tls.acme.enabled: true`, Keel becomes its own certificate manager.

**How ACME http-01 challenge works:**

1. Keel contacts the ACME CA (Let's Encrypt by default) and says "I want a certificate for `api.example.com`."
2. The CA responds with a challenge: "Prove you control `api.example.com` by serving this specific token at
   `http://api.example.com/.well-known/acme-challenge/<token-id>`."
3. Keel registers that token at that path on its HTTP listener (port 80).
4. The CA's servers fetch `http://api.example.com/.well-known/acme-challenge/<token-id>` and verify the
   response.
5. Challenge passes → CA issues the certificate.

**Why port 80 must stay open during ACME:** The http-01 challenge requires plaintext HTTP on port 80. Even if
Keel redirects all other HTTP traffic to HTTPS, the `/.well-known/acme-challenge/` path must be served over
plain HTTP. Keel handles this automatically — it registers the challenge path before any redirect or authn
middleware. The redirect target is always one of `tls.acme.domains`, never an arbitrary client `Host`.

**Automatic renewal:** Keel renews the certificate before it expires (typically when 30 days remain). Renewal
uses the same challenge mechanism. After renewal, Keel hot-reloads the certificate without dropping
connections.

---

## 5. Sidecar Upstream Security

**Build-time opt-out:** `no_sidecar`

When running in sidecar mode, Keel proxies requests to an upstream service. This section covers how Keel
handles headers, IP attribution, response size, and upstream TLS.

### 5.1 Header Forwarding Policy

**Hop-by-hop headers are always stripped.** Per RFC 7230, headers like `Connection`, `Transfer-Encoding`,
`TE`, `Upgrade`, and `Keep-Alive` are specific to a single HTTP connection and must not be forwarded to the
next hop. Keel strips these automatically regardless of any `header_policy` configuration.

**Headers Keel adds outbound:**

- `X-Request-ID` — the request ID Keel uses in its own logs (useful for correlating logs across services;
  see §5.6).
- `Authorization: Bearer <jwt>` — a fresh JWT signed by Keel's `my_signature_key_file`, asserting Keel's
  `my_id`. This replaces any inbound Authorization header.
- `traceparent` — W3C Trace Context for Keel's hop, so the upstream can join the distributed trace (§5.6).
- `Via` — Keel's own entry, appended last (§5.6).

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

`X-Forwarded-For` (XFF) is a de-facto standard header for conveying the original client IP through a chain of
proxies. Each proxy appends the IP of the client it received the request from. So if Client → LB → Keel → App,
the App sees `X-Forwarded-For: <client-ip>, <lb-ip>`.

**The XFF trust problem:** If you blindly trust the leftmost IP in XFF as the real client IP, an attacker can
forge it by sending `X-Forwarded-For: 127.0.0.1` in their request. XFF is only as trustworthy as the proxy
chain you control.

`xff_trusted_hops` tells Keel how many right-most hops in the XFF chain were added by trusted infrastructure
(your load balancers). Keel uses this to find the real client IP:

```text
XFF: attacker-forged-ip, real-client-ip, trusted-lb-ip
xff_trusted_hops: 1

Real client IP = XFF[-1 - trusted_hops] = XFF[-2] = real-client-ip
```

**XFF modes:**

| Mode | Behavior | Use when |
| --- | --- | --- |
| `append` | Add socket peer IP to existing XFF chain | Standard proxy — you want the upstream to see the full chain |
| `replace` | Replace all of XFF with just the client IP | Upstream only needs the real client IP, not the full chain |
| `strip` | Remove XFF entirely | Upstream must not see any client IP (privacy requirement) |

`X-Real-IP` is always set to the trusted client IP (as determined by `xff_trusted_hops`), regardless of XFF mode.

**Normalization:** repeated `X-Forwarded-For` lines are read as one chain, and each entry becomes an IP address in
canonical form (port and IPv6 brackets removed) or `unknown`. An entry carrying another delimiter, a hostname or
junk therefore occupies one position. When the entry `xff_trusted_hops` selects is `unknown`, `X-Real-IP` is the
socket peer.

### 5.3 Response Size Cap

When an upstream response body exceeds `security.max_response_body_bytes` (default 50 MB), Keel:

1. Stops reading the upstream response body.
2. Closes the upstream connection.
3. Returns `502 Bad Gateway` to the client.

**Why truncation returns 502 rather than serving the partial response:** A truncated HTTP body is not valid —
the client would receive a partial response with no indication that it was truncated. 502 makes the truncation
explicit and retriable.

### 5.4 Circuit Breaker

The circuit breaker prevents Keel from hammering a failing upstream and giving failing requests a chance to queue up.

**State machine:**

```text
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

**Why a circuit breaker?** Without one, when an upstream is slow or failing, requests pile up, goroutines pile
up, memory fills, and Keel itself becomes unhealthy. The circuit breaker short-circuits this: as soon as enough
failures are observed, Keel stops making upstream calls and starts returning fast failures. This protects
Keel's memory and allows the upstream time to recover without being bombarded.

**Prometheus metrics:**

- `keel_upstream_health` gauge: 1 = upstream healthy, 0 = unhealthy.
- `keel_circuit_open` gauge: 1 = circuit open, 0 = circuit closed.

### 5.5 Upstream TLS and mTLS

When the upstream is outside the pod (on another host, in another namespace, or on the internet), Keel can
establish a TLS connection with optional mutual TLS (mTLS).

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

**`ca_file`:** If the upstream's TLS certificate was issued by a private CA (not a public CA like Let's
Encrypt), you need to give Keel that CA's certificate so it can verify the upstream. Without this, Keel cannot
establish a TLS connection to a private-CA-signed upstream. Leave empty to use the system trust store
(appropriate for publicly-signed certs).

**Image trust store:** keel's container images are `FROM scratch` and do **not** carry the full public CA
bundle. They trust an explicit allowlist of 10 roots, copied into `/etc/ssl/certs/` from the `keel-certs`
image (`scripts/deploy/docker/Dockerfile.certs`, which holds the complete Mozilla bundle):

| Root(s) | Purpose |
| --- | --- |
| ISRG Root X1, ISRG Root X2 | Let's Encrypt |
| DigiCert Global Root G2, G3 | VeriSign/Symantec successor |
| D-TRUST BR Root CA 1 2020, BR Root CA 2 2023, EV Root CA 1 2020, EV Root CA 2 2023 | Germany (Bundesdruckerei) |
| Telekom Security TLS ECC Root 2020, TLS RSA Root 2023 | Germany (Deutsche Telekom) |

Outbound TLS (ACME, JWKS, OTLP, remote sinks, upstreams) succeeds only if the peer chains to one of these
roots or to a `ca_file` you supply. To change the set, edit the `COPY --from=certs` lists in
`Dockerfile` and `Dockerfile.release` (they must stay identical — `make test-consistency` enforces it).

**mTLS:** When `client_cert_file` and `client_key_file` are set, Keel presents its client certificate during
the TLS handshake with the upstream. This allows the upstream to verify that the connecting client is Keel,
not just any TLS client. Use this when the upstream requires mutual authentication.

**Relationship with service mesh:** If you are running Istio or Linkerd and all service-to-service traffic is
transparently mTLS'd by the mesh, you typically do not need `upstream_tls` for in-mesh traffic. Use
`upstream_tls` for:

- Upstreams outside the mesh boundary (legacy VMs, third-party APIs).
- No-mesh environments.
- Explicit per-upstream trust pinning that you want independent of the mesh.

**`insecure_skip_verify`:** Never set this to true in production. It disables all upstream certificate
verification — Keel will connect to any server claiming to be your upstream, including an attacker performing
a man-in-the-middle attack. The only legitimate use is local development against a self-signed cert when you
cannot install the CA.

### 5.6 Hop Headers

Some headers are only meaningful when the adjacent hop creates them. A client can send its own copy of any of
them, so Keel decides each one itself. A downstream (client-supplied) value that Keel forwards or embeds is
encoded for its destination and occupies exactly one position there.

A **trusted hop** is infrastructure in front of Keel that sets these headers itself: Keel has one when
`sidecar.xff_trusted_hops` is greater than 0.

| Header | Keel's policy | Origin |
| --- | --- | --- |
| `Authorization` | With outbound signing on, Keel sends only its own signed JWT, whose claims carry no client data. With signing off, the client's JWT reaches the upstream byte-identical, and Keel adds nothing that vouches for it. A request with two `Authorization` lines is refused with 400. | Generated by Keel (signing on), relayed by design (signing off) |
| `X-Real-IP` | Always set from the client IP Keel computes (§5.2). | Generated by Keel |
| `X-Forwarded-For` | Repeated lines are read as one chain. Each entry is normalized to an IP address in canonical form, or `unknown`; then the configured mode applies (§5.2). | Relayed by design (`append`), generated by Keel (`replace`), stripped (`strip`) |
| `Forwarded`, `X-Forwarded-Host`, `X-Forwarded-Proto` | Removed before the request leaves Keel. | Stripped |
| `Via` | Keel appends its own entry, `<protocol> <authn.my_id or "keel">`, as the last one. Earlier entries are the earlier hops' claims. | Relayed by design, plus Keel's entry |
| `traceparent`, `tracestate` | From a trusted hop, a single valid `traceparent` (lowercase hex IDs, not all zero) continues the trace with a new span ID for Keel's hop. Otherwise Keel starts a new trace. The upstream receives Keel's own `traceparent`. | Generated by Keel, trace ID relayed from a trusted hop |
| `x-request-id` | From a trusted hop, a single valid ID (1–128 characters of `A-Za-z0-9._:-`) is kept. Otherwise Keel assigns a ULID. The upstream receives the ID Keel uses in its logs. | Generated by Keel, relayed from a trusted hop |

**Where client values go, and how each is encoded:**

| Destination | Encoding |
| --- | --- |
| Outbound headers | The policies above. Keel builds `X-Forwarded-For`, `Via` and `traceparent` only from validated parts. |
| External authorization input (§6.2) | JSON, with each header field line as its own array element. |
| Logs | JSON mode: JSON strings. Text mode: control characters escaped. Every entry is one line. |
| Trace spans | JSON (OTLP/HTTP). The span name and attributes are encoded as strings. |
| Keel's outbound JWT | Fixed claims (`iss`, `sub`, `iat`, `exp`, `aud`) from Keel's configuration only. |

---

## 6. External Authorization

**Build-time opt-out:** `no_authz`

Keel can delegate authorization decisions to an external policy engine via `ext_authz`. When enabled, every
inbound request is forwarded to the configured endpoint before reaching the upstream service. The endpoint
returns allow or deny; Keel enforces the decision with no application code changes required.

This is distinct from the authn layer (§2). Authn establishes *who* the caller is (JWT validation). ExtAuthz
asks an external system *whether that caller may perform this action* — a question that
may require policy context Keel does not have locally.

### 6.1 How It Works

1. Keel receives an inbound request and runs the authn layer (if enabled).
2. Keel POSTs a JSON envelope to `ext_authz.endpoint` containing method, path, query string, flattened
   headers, and remote address.
3. The decision endpoint returns allow or deny.
4. On allow, the request proceeds to the upstream. On deny, Keel returns 403.
5. On network error or timeout, behaviour is governed by `fail_open`.

### 6.2 Transport Modes

**`transport: "http"` (default)**

Keel sends the request envelope as a flat JSON object. A 200 response allows the request; any other status
denies it. Use this for custom authz services, Envoy ext_authz HTTP API, and any policy engine with a simple
HTTP interface.

Request body:

```json
{
  "method":  "GET",
  "path":    "/api/resource",
  "query":   "page=1",
  "headers": { "authorization": ["Bearer ..."], "x-request-id": ["abc"] },
  "remote":  "10.0.0.5:54321"
}
```

`headers` maps each lowercase header name to an array with one element per header field line, so a policy can
tell `X-Role: a,b` (one line, one element `"a,b"`) from `X-Role: a` plus `X-Role: b` (two elements). A Rego policy
reads a single-valued header as `input.headers["x-role"][0]`.

**`transport: "opa"`**

Keel wraps the envelope in `{"input": ...}` per OPA convention and POSTs to the OPA policy evaluation
endpoint (e.g. `/v1/data/authz/allow`). The response is parsed for `{"result": true/false}`. A `true` result
allows the request.

### 6.3 Unix Socket Endpoints

For lowest-latency in-pod decisions (e.g., OPA running as a sidecar), use a unix socket:

```yaml
ext_authz:
  endpoint: "unix:///run/opa/opa.sock"
  path: "/v1/data/authz/allow"
  transport: "opa"
```

The `endpoint` field carries the socket path. The `path` field is the HTTP request path sent over the socket.
There is no TCP overhead.

### 6.4 Configuration Reference

```yaml
ext_authz:
  enabled: false                              # off by default
  endpoint: "http://authz-service:8080/check" # or unix:///path/to/socket
  path: ""                                    # request path for unix socket endpoints
  transport: "http"                           # "http" or "opa"
  timeout: "500ms"
  fail_open: false                            # deny on error (safest default)
```

ENV var overrides: `KEEL_AUTHZ`, `KEEL_AUTHZ_ENDPOINT`, `KEEL_AUTHZ_PATH`, `KEEL_AUTHZ_TIMEOUT`,
`KEEL_AUTHZ_TRANSPORT`, `KEEL_AUTHZ_FAIL_OPEN`.

### 6.5 Binary Footprint

When the `no_authz` build tag is set, the entire ext_authz package is excluded at compile time. Binary size is
unaffected. This mirrors the `no_authn`, `no_h3`, and other opt-out tags.

```sh
go build -tags no_authz ./cmd/keel
```

### 6.6 Fail-Open vs Fail-Closed

`fail_open: false` (default) — deny the request if the authz endpoint is unreachable or times out. Safe
default: a misconfigured or crashed policy engine blocks access rather than granting it.

`fail_open: true` — allow the request on error. Use only when the authz endpoint is advisory and availability
is more important than strict enforcement.

### 6.7 Examples

See [`examples/authz/`](../examples/authz/) for ready-to-use configurations:

- `opa-http.yaml` — OPA sidecar over HTTP
- `opa-unix-socket.yaml` — OPA sidecar over unix socket (lowest latency)
- `custom-http.yaml` — Custom authz service or Envoy ext_authz HTTP API

---

## 7. Supply Chain Verification

Every Keel release publishes signed artifacts and OCI attestations via [Sigstore](https://sigstore.dev)
(keyless, OIDC-based). No key management required to verify.

### 7.1 Verify a Binary

```sh
cosign verify-blob \
  --certificate-identity-regexp '^https://github\.com/keelcore/keel/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  --bundle keel-min-linux-amd64.bundle \
  keel-min-linux-amd64
```

Binaries ship for `linux/amd64` and `linux/arm64` (`keel-<flavor>-linux-<arch>`).

Bundles are attached to each [GitHub Release](https://github.com/keelcore/keel/releases) alongside the binaries,
each under two names with identical content: `<artifact>.bundle` and `<artifact>.sigstore.json` (the extension
OpenSSF Scorecard recognizes). The release also carries `keel.intoto.jsonl`, the SLSA v1 provenance DSSE envelope
covering every binary, for `slsa-verifier` and other in-toto tooling.

### 7.2 Verify a Container Image

Each tag (`vX.Y.Z`, `-min`, `-fips`, and the `vX.Y`, `vX`, `latest` aliases) is a multi-arch index for `linux/amd64`
and `linux/arm64`. The index and each per-architecture image are signed:

```sh
cosign verify \
  --certificate-identity-regexp '^https://github\.com/keelcore/keel/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  ghcr.io/keelcore/keel:latest
```

### 7.3 Inspect the SBOM Attestation

Each per-architecture image carries an SPDX SBOM generated from that image. Resolve the platform digest, then:

```sh
digest="$(docker buildx imagetools inspect ghcr.io/keelcore/keel:latest --raw \
  | jq -r '.manifests[] | select(.platform.architecture == "amd64") | .digest')"
cosign verify-attestation \
  --type 'https://spdx.dev/Document' \
  --certificate-identity-regexp '^https://github\.com/keelcore/keel/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  "ghcr.io/keelcore/keel@${digest}" \
  | jq '.payload | @base64d | fromjson | .predicate'
```

The SBOM is in SPDX JSON format and lists the Go modules compiled into that image's binary.

### 7.4 Verify SLSA Provenance

```sh
gh attestation verify oci://ghcr.io/keelcore/keel:latest --repo keelcore/keel
gh attestation verify keel-min-linux-amd64 --repo keelcore/keel
```

SLSA provenance is generated by
[`actions/attest-build-provenance`](https://github.com/actions/attest-build-provenance) for every release binary and
every image index, and links each one back to the exact workflow run and commit that produced it.

### 7.5 Read the VEX Statements

Each image index carries an [OpenVEX](https://openvex.dev) attestation stating which reported vulnerabilities do not
affect keel and why. Each release generates it with `govulncheck -format openvex` (`make release-vex`): every
vulnerability govulncheck finds in a required module but cannot reach from keel's code is stated `not_affected`. The
same document is attached to the [GitHub Release](https://github.com/keelcore/keel/releases) as `keel.openvex.json`.

```sh
cosign verify-attestation \
  --type openvex \
  --certificate-identity-regexp '^https://github\.com/keelcore/keel/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  ghcr.io/keelcore/keel:latest \
  | jq '.payload | @base64d | fromjson | .predicate.statements'
```

Every release verifies all of the above for its own artifacts as the last step of the release workflow
(`make release-verify`).

---

## 8. Protocol Rejections

Keel serves TLS 1.3 only, with ALPN `h2` and `http/1.1` (FIPS builds: P-256 and P-384 key exchange, AES-GCM
suites), and optionally requires a client certificate or a minimum HTTP version. A client below that floor learns
why, and what to upgrade, through a stable code (`KEEL-PROTO-NNN`, [errors.md](errors.md)).

**The configuration decides what is rejected.** Keel classifies each ClientHello against the TLS policy it built
for the listener, never against fixed constants, so a protocol the configuration allows is served normally, with no
`Keel-Error-Code`.

| Class | What happens | Code |
| --- | --- | --- |
| TLS below 1.3 | Handshake refused; logged and counted | `KEEL-PROTO-001` |
| No shared ALPN | Handshake refused; logged and counted | `KEEL-PROTO-002` |
| No shared group | Handshake refused; logged and counted | `KEEL-PROTO-003` |
| No shared TLS 1.3 cipher | Handshake refused; logged and counted | `KEEL-PROTO-004` |
| mTLS, no client certificate | Handshake refused; with `tls.client_cert_notice: true`, 301 to the notice | `KEEL-PROTO-005` |
| Plain HTTP on the HTTPS port | 301 to the notice | `KEEL-PROTO-006` |
| HTTP below `security.min_http_version` | 301 to the notice | `KEEL-PROTO-007` |

Keel completes TLS 1.3 handshakes only; every TLS 1.2 or older hello is refused at the handshake, so protocol
scanners see TLS 1.3 alone.

**The 301.** `Location` is the notice page, `/.well-known/keel/upgrade?code=<code>`, under `tls.notice.base_url`,
or else on the plain-HTTP listener of the host the client asked for. The body is an RFC 9457 problem
(`application/problem+json`) carrying the code, so automation that does not follow redirects reads it. Every
rejection carries `Cache-Control: no-store` (a cached 301 would outlive the client's upgrade) and closes the
connection. With no HTTP listener and no `base_url`, keel answers `426 Upgrade Required` with the same problem.

**The notice page** is plain HTTP on purpose: a client whose TLS cannot reach the HTTPS listener can still read it.
It is built from the code alone (plus the operator's `tls.notice.help_url` and `tls.notice.contact`, escaped) and
reflects nothing else from the request. It runs before authentication and is served for `GET` and `HEAD`.

**HTTP/3 discovery.** With `listeners.h3` enabled, every HTTPS response below HTTP/3 carries
`Alt-Svc: h3=":<h3 port>"; ma=<security.alt_svc_max_age>`, so h3-capable clients move to HTTP/3 on their next
request.

**mTLS notice (`tls.client_cert_notice`).** With `tls.client_ca_file` set, the handshake completes when the client
presents no certificate, and every request on that connection gets the `KEEL-PROTO-005` 301. A presented
certificate is still verified during the handshake, and an invalid one still fails it.

**Observability.**

- Log `tls_handshake_rejected` (info): `code`, `reason`, `client_max_version`, `offered_alpn`, `sni`, `remote`; at
  most 10 lines a second.
- Counters: `keel_tls_handshake_rejections_total{code}` (every refused handshake),
  `keel_protocol_rejections_total{code}` (301s sent), `keel_protocol_notices_served_total{code,format}`.
- net/http's own server errors (handshake failures, malformed requests) are logged as `http_server_error` at debug.
