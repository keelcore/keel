# Keel Deployment Guide

This document covers all deployment patterns: library mode (embedding Keel in your Go service), the Helm chart (all modes, values reference, ServiceMonitor, NetworkPolicy, PDB), and the Docker Compose test harness.

---

## 1. Library Mode: Embedding Keel in Your Go Service

Library mode is the tightest integration. Your Go application links Keel as a dependency, registers its own HTTP handlers, and calls `srv.Run(ctx)`. Keel starts the listeners, wires up TLS, authn, OWASP middleware, health endpoints, tracing, and metrics — all configured by the shared config struct.

**When to choose library mode:**
- Your service is already written in Go.
- You want the lowest possible request latency (no proxy hop).
- You want to use Keel's middleware independently on your own handlers.
- You want one binary that is both your application and the TLS/authn layer.

### 1.1 Wrap the Keel Config in Your App Config

Keel's configuration struct is designed to nest cleanly inside your application's own configuration. You declare an `AppConfig` struct that embeds `keelconfig.Config` under a `keel:` YAML key:

```go
import keelconfig "github.com/keelcore/keel/pkg/config"

// AppConfig is your application's full configuration struct.
// It nests Keel's config under the "keel" key so your app's
// keel.yaml can have both app-specific and keel-specific settings.
type AppConfig struct {
    App  AppSettings       `yaml:"app"`
    Keel keelconfig.Config `yaml:"keel"`
}

// Start with Keel's defaults pre-populated.
// This is critical: if you unmarshal YAML onto a zero-value struct,
// any key absent from your YAML gets its zero value (false, 0, "")
// rather than Keel's intended default (true, 8080, etc.).
cfg := AppConfig{Keel: keelconfig.Defaults()}

// Now unmarshal your YAML on top of the defaults.
// Keys present in your YAML override defaults; absent keys retain defaults.
data, err := os.ReadFile("keel.yaml")
if err != nil { log.Fatal(err) }
if err := yaml.Unmarshal(data, &cfg); err != nil { log.Fatal(err) }
```

After loading, validate and finalize the Keel config:

```go
keel, err := keelconfig.From(&cfg.Keel)
if err != nil { log.Fatal(err) }
cfg.Keel = keel
```

### 1.2 Create and Run the Server

```go
import (
    keelcore "github.com/keelcore/keel/pkg/core"
    "github.com/keelcore/keel/pkg/core/logging"
    "github.com/keelcore/keel/pkg/core/ports"
)

// Create a logger. Keel uses structured JSON logging by default.
log := logging.New(logging.Config{JSON: cfg.Keel.Logging.JSON})

// Create the server. This wires up TLS, middleware, health endpoints,
// Prometheus, tracing — everything. It does not start listening yet.
srv := keelcore.NewServer(log, cfg.Keel)

// Register your application routes.
// ports.HTTPS is the constant for the main HTTPS listener port.
// Keel automatically wires your handler through OWASP, authn,
// request ID, and access log middleware.
srv.AddRoute(ports.HTTPS, "GET /api/v1/items", http.HandlerFunc(listItems))
srv.AddRoute(ports.HTTPS, "POST /api/v1/items", http.HandlerFunc(createItem))

// Register readiness checks for your dependencies.
// Keel calls these periodically and reflects their results in /readyz.
srv.AddReadinessCheck("db", func(ctx context.Context) error {
    return db.PingContext(ctx)
})

// Set up a context with cancellation. Keel shuts down when ctx is cancelled.
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

// Run blocks until ctx is cancelled or a fatal error occurs.
// On SIGTERM/SIGINT, Keel triggers graceful shutdown automatically.
if err := srv.Run(ctx); err != nil {
    log.Error("server exited with error", "err", err)
    os.Exit(1)
}
```

### 1.3 Using Keel Middleware Independently

Keel exports its middleware pipeline so you can apply individual middleware to handlers outside of the main server, or compose them in a different order:

```go
import "github.com/keelcore/keel/pkg/core/mw"

// Apply individual middleware to your own handler.
// Middleware is applied from outermost to innermost, so
// RequestID runs first, then AccessLog, then OWASP, then your handler.
h := mw.RequestID(
         mw.AccessLog(logger,
         mw.OWASP(cfg,
         yourHandler)))

// Or as a chain:
h := mw.Chain(yourHandler,
    mw.RequestID,
    mw.AccessLog(logger),
    mw.OWASP(cfg),
)
```

### 1.4 Your keel.yaml Nests Under `keel:`

When using library mode with a nested config, your YAML structure looks like:

```yaml
# keel.yaml — your application's config file
app:
  name: myapp
  database_url: postgres://localhost/myapp

# The "keel" key is where all Keel-specific config lives.
# This nests cleanly without any conflict with your app's own keys.
keel:
  listeners:
    https:
      enabled: true
      port: 8443
    health:
      enabled: true
      port: 9091
    ready:
      enabled: true
      port: 9092
    admin:
      enabled: true
      port: 9999
  tls:
    cert_file: /etc/myapp/tls.crt
    key_file:  /etc/myapp/tls.key
  authn:
    enabled: true
    my_id: myapp
    trusted_ids:
      - service-a
      - service-b
  logging:
    json: true
    level: info
  metrics:
    prometheus: true
  tracing:
    otlp:
      enabled: true
      endpoint: otel-collector:4317
      insecure: true
```

### 1.5 Extension Model: Route Registration

```go
// WithRegistrar allows you to define route groups in a separate package,
// keeping your main.go clean and the server setup declarative.
srv := keel.New(
    keel.WithConfig(cfg),
    keel.WithRegistrar(myapi.NewRegistrar()),    // registers /api/v1/* routes
    keel.WithRegistrar(admin.NewRegistrar()),    // registers /admin/* routes
    keel.WithRoute(ports.HTTP, "/ping", pingHandler),   // inline one-off routes
)
srv.Run(ctx)
```

**Built-in default route:** If no user route claims the root path on the HTTP port, Keel serves a built-in default response. This guarantees that the service responds to health checks and smoke tests even before you add your own routes. You can override it by registering a handler for `/`.

---

## 2. Helm Chart

### 2.1 What Helm Does

Helm is the package manager for Kubernetes. A Helm chart is a template for a set of Kubernetes YAML manifests (Deployments, Services, ConfigMaps, Secrets, etc.). Helm lets you parameterize these manifests using a `values.yaml` file, so you can deploy the same chart to development, staging, and production with different settings.

The Keel Helm chart supports three deployment modes (library, sidecar intra-pod, sidecar out-of-pod) and generates all the Kubernetes resources needed for each.

### 2.2 Image Flavors

```yaml
# values.yaml
image:
  flavor: default   # default | min | fips | custom
```

| Flavor | Build tags | Binary size | Use when |
|---|---|---|---|
| `default` | none | ~5–8 MB | All features; standard production deployment |
| `min` | `no_otel,no_statsd,no_remotelog,no_h3` | ~3–4 MB | Minimal footprint where observability comes from the platform |
| `fips` | FIPS boringcrypto build | ~6–10 MB | Compliance environments (FedRAMP, HIPAA, PCI) requiring FIPS 140-2/3 validated cryptography |
| `custom` | user-defined | varies | When you build your own binary with custom tags and want to use the Keel chart |

For `custom`, set:
```yaml
image:
  flavor: custom
  repository: myregistry.example.com/mykeel
  tag: "1.2.3-custom"
```

### 2.3 Library Mode

```yaml
# values.yaml
mode: library

# Keel config — becomes a ConfigMap mounted at /etc/keel/keel.yaml
config:
  listeners:
    https:
      enabled: true
      port: 8443
    health:
      enabled: true
      port: 9091
    ready:
      enabled: true
      port: 9092
  authn:
    enabled: true
    my_id: myapp
  logging:
    json: true
  metrics:
    prometheus: true

# Secrets — contents become a Kubernetes Secret, mounted at /etc/keel/secrets/
secrets:
  existingSecret: myapp-keel-secrets    # Use an existing k8s Secret
  # OR provide values directly (not recommended for production):
  # tlsCert: <base64-encoded-cert>
  # tlsKey: <base64-encoded-key>
```

### 2.4 Sidecar Mode — Intra-Pod

In intra-pod sidecar mode, Keel runs as a sidecar container in the same pod as your application. The application listens on `localhost:<port>` over plain HTTP; Keel owns all external-facing ports.

```yaml
# values.yaml
mode: sidecar

sidecar:
  app:
    image: mycompany/myapp:1.0    # Your application container image
    port: 3000                    # Port your app listens on (localhost only)
    env:                          # Optional env vars for your app container
      - name: DATABASE_URL
        valueFrom:
          secretKeyRef:
            name: myapp-secrets
            key: database_url

  upstream_url: http://127.0.0.1:3000   # Keel proxies to this URL
```

The chart generates a Pod with two containers: your app container and Keel. The app container is not exposed via the Service — only Keel's ports are exposed.

### 2.5 Sidecar Mode — Out-of-Pod

In out-of-pod sidecar mode, Keel runs as a standalone pod and proxies to an upstream running on a different host (legacy VM, another namespace, external API).

```yaml
# values.yaml
mode: sidecar

sidecar:
  upstream_url: https://legacy-api.internal:8443
  upstream_tls:
    enabled: true
    ca_file: /etc/keel/secrets/upstream-ca.crt
    client_cert_file: /etc/keel/secrets/client.crt
    client_key_file: /etc/keel/secrets/client.key
  upstreamTLSSecret: my-upstream-mtls-secret   # k8s Secret name containing the mTLS material
```

### 2.6 Full Values Reference

```yaml
# values.yaml — full reference with all supported keys

# --- Image ---
image:
  flavor: default         # default | min | fips | custom
  repository: ""          # Only for flavor: custom
  tag: ""                 # Only for flavor: custom
  pullPolicy: IfNotPresent

# --- Deployment mode ---
mode: library             # library | sidecar

# --- Replicas and scaling ---
replicaCount: 2           # Number of pod replicas

autoscaling:
  enabled: false
  minReplicas: 2
  maxReplicas: 10
  targetCPUUtilizationPercentage: 80
  targetMemoryUtilizationPercentage: 80

# --- Keel config (becomes a ConfigMap) ---
config: {}                # Keel YAML config, nested under key "keel:" in the ConfigMap

# --- Secrets ---
secrets:
  existingSecret: ""      # Name of an existing k8s Secret to mount
  mountPath: /etc/keel/secrets

# --- Service ---
service:
  type: ClusterIP
  ports:
    http: 8080
    https: 8443
    health: 9091
    ready: 9092
    startup: 9093
    admin: 9999

# --- Sidecar config (only when mode: sidecar) ---
sidecar:
  app:
    image: ""
    port: 3000
    env: []
    resources: {}
  upstream_url: ""
  upstream_tls:
    enabled: false
    ca_file: ""
    client_cert_file: ""
    client_key_file: ""
  upstreamTLSSecret: ""

# --- Resources ---
resources:
  requests:
    cpu: 100m
    memory: 64Mi
  limits:
    cpu: 500m
    memory: 128Mi

# --- Pod settings ---
podAnnotations: {}
podSecurityContext:
  runAsNonRoot: true
  runAsUser: 65534
  fsGroup: 65534
  seccompProfile:
    type: RuntimeDefault
securityContext:
  allowPrivilegeEscalation: false
  capabilities:
    drop: ["ALL"]
  readOnlyRootFilesystem: true

# --- Probes ---
livenessProbe:
  httpGet:
    path: /healthz
    port: health
  initialDelaySeconds: 5
  periodSeconds: 10
  failureThreshold: 3

readinessProbe:
  httpGet:
    path: /readyz
    port: ready
  initialDelaySeconds: 5
  periodSeconds: 5
  failureThreshold: 3

startupProbe:
  httpGet:
    path: /startupz
    port: startup
  failureThreshold: 30
  periodSeconds: 10

# --- Lifecycle ---
lifecycle:
  preStop:
    exec:
      command: ["/bin/sh", "-c", "sleep 2"]

terminationGracePeriodSeconds: 30

# --- ServiceMonitor (Prometheus Operator) ---
serviceMonitor:
  enabled: false          # Set true if you have the Prometheus Operator installed
  labels: {}              # Labels to add to the ServiceMonitor (must match Prometheus selector)
  interval: 30s
  scrapeTimeout: 10s

# --- NetworkPolicy ---
networkPolicy:
  enabled: false          # Set true to restrict ingress/egress

# --- PodDisruptionBudget ---
podDisruptionBudget:
  enabled: true
  minAvailable: 1         # OR use maxUnavailable: 1

# --- Node selection ---
nodeSelector: {}
tolerations: []
affinity: {}

# --- Extra volumes and mounts ---
extraVolumes: []
extraVolumeMounts: []
```

### 2.7 ServiceMonitor (Prometheus Operator)

**What a ServiceMonitor is:** The Prometheus Operator is a Kubernetes operator that manages Prometheus instances. Instead of configuring Prometheus scrape targets directly in a ConfigMap, you create `ServiceMonitor` custom resources that the operator picks up and translates into Prometheus scrape configs. This lets you manage monitoring configuration as Kubernetes-native resources.

Enable the ServiceMonitor in Helm:
```yaml
serviceMonitor:
  enabled: true
  labels:
    release: prometheus   # Must match your Prometheus Operator's serviceMonitorSelector
  interval: 30s
```

The chart generates:
```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: keel
  labels:
    release: prometheus
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: keel
  endpoints:
    - port: admin
      path: /metrics
      interval: 30s
      scrapeTimeout: 10s
```

### 2.8 NetworkPolicy

**What a NetworkPolicy is:** A Kubernetes NetworkPolicy restricts which pods can send network traffic to which other pods. Without any NetworkPolicy, all pods in a cluster can communicate with all other pods (flat network). NetworkPolicies add a firewall layer.

Enable and configure:
```yaml
networkPolicy:
  enabled: true
```

The chart generates a NetworkPolicy that:
- Allows ingress to HTTP/HTTPS ports from anywhere (or from a specified ingress controller namespace).
- Allows ingress to health/ready ports from the kubelet (any node IP).
- Allows ingress to the admin port only from within the same namespace.
- Allows all egress (for upstream calls, ACME, OTLP, etc.).

Customize for your environment by using `extraVolumes` and pod annotations if you need tighter egress control.

### 2.9 PodDisruptionBudget

**What a PDB is:** A PodDisruptionBudget tells Kubernetes how many pods of a given Deployment can be unavailable at once during a "voluntary disruption" — node drain, cluster upgrade, or manual scale-down. Without a PDB, a node drain could simultaneously evict all replicas of your service, causing downtime.

```yaml
podDisruptionBudget:
  enabled: true
  minAvailable: 1   # At least 1 replica must be available at all times
```

With `replicaCount: 2` and `minAvailable: 1`, a node drain can only evict one pod at a time, waiting for the replacement pod to become ready before evicting the second.

### 2.10 Scaling Signals

For Horizontal Pod Autoscaler (HPA) scaling, the recommended signals are:

```yaml
# CPU-based scaling (works without custom metrics)
autoscaling:
  enabled: true
  minReplicas: 2
  maxReplicas: 10
  targetCPUUtilizationPercentage: 80
```

For more sophisticated scaling using Keel metrics (requires KEDA or Prometheus Adapter):

```yaml
# Scale on in-flight request count
# keel_requests_inflight > threshold → scale up
# Requires: Prometheus Adapter or KEDA with Prometheus trigger
```

Example KEDA ScaledObject:
```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: keel
spec:
  scaleTargetRef:
    name: keel
  minReplicaCount: 2
  maxReplicaCount: 20
  triggers:
    - type: prometheus
      metadata:
        serverAddress: http://prometheus:9090
        metricName: keel_requests_inflight
        threshold: "50"           # Scale up when > 50 in-flight requests per replica
        query: keel_requests_inflight
```

---

## 3. Docker Compose Test Harness

The Docker Compose test harness runs Keel together with a mock upstream, Prometheus, an OpenTelemetry collector, and Jaeger for end-to-end integration testing. It is the canonical way to run integration tests locally and in CI.

### 3.1 Why Docker Compose for Testing

Unit tests verify individual functions in isolation. Integration tests verify that the components work together correctly: Keel must actually accept a TLS connection, validate a JWT, proxy the request to the upstream, emit a trace, and expose the right Prometheus metric. Docker Compose lets you run the full stack locally with a single command.

### 3.2 Full Compose File

```yaml
# tests/compose/docker-compose.test.yaml

services:
  # The upstream service that Keel proxies to.
  # hashicorp/http-echo is a minimal HTTP server that returns a fixed text response.
  # It simulates your application without requiring you to build your app just for tests.
  upstream:
    image: hashicorp/http-echo:latest
    command: ["-text=upstream-ok", "-listen=:3000"]
    ports:
      - "3000:3000"

  # Keel itself.
  keel:
    build:
      context: ../..
      dockerfile: build/docker/Dockerfile
    environment:
      KEEL_CONFIG: /etc/keel/keel.yaml
      KEEL_SECRETS: /etc/keel/secrets/keel-secrets.yaml
    volumes:
      - ./tests/fixtures/keel.yaml:/etc/keel/keel.yaml:ro
      - ./tests/fixtures/secrets:/etc/keel/secrets:ro
      - ./tests/fixtures/certs:/etc/keel/tls:ro
    ports:
      - "8080:8080"     # HTTP
      - "8443:8443"     # HTTPS
      - "9091:9091"     # Liveness probe
      - "9092:9092"     # Readiness probe
      - "9093:9093"     # Startup probe
      - "9999:9999"     # Admin port
    depends_on:
      - upstream
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:9091/healthz"]
      interval: 5s
      timeout: 3s
      retries: 10

  # Prometheus — scrapes Keel's /metrics endpoint every 15 seconds.
  # Access the Prometheus UI at http://localhost:9292.
  prometheus:
    image: prom/prometheus:latest
    volumes:
      - ./tests/fixtures/prometheus.yml:/etc/prometheus/prometheus.yml:ro
    ports:
      - "9292:9090"
    command:
      - '--config.file=/etc/prometheus/prometheus.yml'

  # OpenTelemetry Collector — receives OTLP traces from Keel and exports to Jaeger.
  otel-collector:
    image: otel/opentelemetry-collector-contrib:latest
    volumes:
      - ./tests/fixtures/otel-collector.yaml:/etc/otel/config.yaml:ro
    command: ["--config=/etc/otel/config.yaml"]
    ports:
      - "4317:4317"     # OTLP gRPC
      - "4318:4318"     # OTLP HTTP
    depends_on:
      - jaeger

  # Jaeger — trace storage and UI.
  # Access the Jaeger UI at http://localhost:16686.
  jaeger:
    image: jaegertracing/all-in-one:latest
    ports:
      - "16686:16686"   # Jaeger UI
    environment:
      COLLECTOR_OTLP_ENABLED: "true"
```

### 3.3 Docker Compose Networking

**How Docker Compose networking works:** By default, Docker Compose creates a private network for all services in the same Compose file. Services can reach each other using their service names as hostnames. For example, the `keel` service can reach the `upstream` service at `http://upstream:3000`.

This is why the Keel config in `tests/fixtures/keel.yaml` uses `upstream_url: http://upstream:3000` — `upstream` resolves to the upstream container's IP on the Compose network. On the host machine, you reach them via `localhost:<mapped-port>`.

**Host port mappings:** The `ports` directives map `<host>:<container>` ports. `"8443:8443"` means port 8443 on your Mac/Linux machine maps to port 8443 inside the Keel container.

### 3.4 Running the Test Harness

```sh
# Run integration tests (requires Docker + Docker Compose)
KEEL_COMPOSE_TESTS=1 go test ./tests/compose/...

# Or use the script (also starts/stops Compose automatically)
./scripts/test/compose.sh

# Or use make
make test-compose
```

The `KEEL_COMPOSE_TESTS=1` guard prevents the integration tests from running in unit test mode (where Docker is not available).

### 3.5 Test Fixtures

Test certificates are generated by:
```sh
./tests/fixtures/gen-certs.sh
```

The output (`tests/fixtures/certs/`) is gitignored — never commit test certificates to the repository. Each developer or CI job generates their own test certificates.

The fixtures directory contains:
- `tests/fixtures/keel.yaml` — Keel config for the test harness (sidecar mode, pointing to upstream)
- `tests/fixtures/secrets/keel-secrets.yaml` — Test secrets (test signing keys, test cert paths)
- `tests/fixtures/prometheus.yml` — Prometheus scrape config
- `tests/fixtures/otel-collector.yaml` — OpenTelemetry Collector pipeline config