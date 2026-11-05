# Architecture

How a request, a certificate, a telemetry signal and a shutdown move through Keel. Policy details live in
[security.md](security.md) and [operations.md](operations.md).

## 1. North/South Request (Sidecar)

Every step runs in this order for HTTP, HTTPS and HTTP/3 listeners (`Server.wrapMain` in `pkg/core/server.go`). Health,
readiness, startup and admin listeners have their own muxes and skip this chain.

```mermaid
flowchart TD
    C[Client] --> T{TLS handshake}
    T -->|below policy| TR[Refused: logged and counted, KEEL-PROTO code]
    T -->|ok| M[Metrics, statsd]
    M --> S{Memory headroom and concurrency}
    S -->|exhausted| R503[503]
    S -->|ok| H{Hop headers}
    H -->|two Authorization lines| R400[400]
    H -->|ok| TC[Trace context, span, request ID, access log]
    TC --> O[OWASP headers and limits, Alt-Svc]
    O --> P{Protocol policy}
    P -->|below minimum, or no client cert with notice on| R301[301 to the notice page]
    P -->|notice path on plain HTTP| N[Notice page]
    P -->|ok| SH{Backpressure shedding}
    SH -->|shedding| R503
    SH -->|ok| A{Authn: JWT}
    A -->|missing or invalid| R401[401]
    A -->|sub not allowed| R403[403]
    A -->|ok| Z{Authz: OPA or ext_authz}
    Z -->|deny| R403
    Z -->|unreachable, fail_open false| R403
    Z -->|allow, or unreachable with fail_open true| HP[XFF, Via, trace, request ID, header policy]
    HP --> SG[Outbound signing]
    SG --> CB{Circuit breaker}
    CB -->|open| R502[502]
    CB -->|closed or probe| U[Upstream: dial retry, Retry-After]
    U -->|dial failed or no valid response| R502
    U -->|response| APP[Response streamed to the client]
```

When a JWKS endpoint is unreachable, Keel keeps serving the last good keys until the failure backoff ends; with no
good keys, the token fails with 401. An expired certificate is served until it is replaced: ACME renews before expiry,
and `keel_tls_cert_expiry_seconds` alerts first.

## 2. Library Mode

The same chain, inside the application's binary. The application registers handlers instead of proxying.

```mermaid
flowchart LR
    C[Client] --> L[Keel listeners and TLS]
    subgraph Binary[Application binary]
        L --> W[Keel middleware chain, as in section 1]
        W --> R[Router, by listener port]
        R --> H[Application handlers: WithHandler, AddRoute]
    end
```

## 3. Certificate Lifecycle

```mermaid
flowchart TD
    ST[Start] --> CL{Cached certificate valid for the configured domains?}
    CL -->|yes| SV[Serve it]
    CL -->|no| OR[ACME order: http-01 challenge on port 80]
    OR -->|issued| SO[Store key and chain in cache_dir]
    OR -->|failed| RT[Log, retry]
    RT --> OR
    SO --> SV
    SV --> LP{Within 30 days of expiry?}
    LP -->|no| WT[Wait for the renewal window]
    WT --> LP
    LP -->|yes| OR
```

Certificates from files take the same path to the listeners through `CertLoader`; SIGHUP reloads them.

## 4. Observability Fan-Out

```mermaid
flowchart LR
    K[Keel] -->|scrape /metrics on the admin port| P[Prometheus]
    K -->|OTLP/HTTP| O[OTel collector]
    K -->|UDP| S[statsd]
    K -->|stdout, JSON or text| L[Container log]
    K -->|syslog over TCP, or HTTP batches| R[Remote log sink]
```

Outbound HTTP and TLS use one policy (`pkg/core/tlspolicy`). The remote sink and the OTLP exporter buffer without
blocking requests, and count what they drop.

## 5. Shutdown

```mermaid
sequenceDiagram
    participant O as Orchestrator
    participant K as Keel
    participant LB as Load balancer
    O->>K: SIGTERM
    K->>K: /readyz reports not ready
    LB->>K: Stops routing new traffic
    K->>K: Prestop sleep (timeouts.prestop_sleep)
    K->>K: Drain listeners (timeouts.shutdown_drain)
    K->>O: Exit 0
    O-->>K: SIGKILL if terminationGracePeriodSeconds passes first
```

Keep `terminationGracePeriodSeconds` above `prestop_sleep` plus `shutdown_drain`, so the drain finishes before SIGKILL.
