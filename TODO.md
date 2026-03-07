# TODO.md

## Target Audience
* **Corporate Users**: Infrastructure teams requiring 10+ year stability, FIPS compatibility, and "no-pager" operational excellence.
* **Gen Z "BYOS"**: Independent developers seeking instant-on security, tiny footprints (~5–8 MB), and high-performance "cool factor".

---

## Priority Ordering

Work proceeds in the order below. Nothing in a later phase starts until the prior phase is merged and human-reviewed.

---

## P0 — README (DONE)
* [x] Comprehensive README.md as single source of truth for all features (implemented and planned).
* [x] ACME/Let's Encrypt constraint documented (http-01 challenge route on plain HTTP before authn/redirect).
* [x] Full YAML config schema defined (keel.yaml).
* [x] YAML secrets file pattern defined (keel-secrets.yaml, k8s Secret bind mount).
* [x] Docker Compose test harness topology documented.
* [x] All recommended features documented (sidecar behaviors, circuit breaker, XFF, access log, /version, /debug/pprof, etc.).
* [x] Build-tag table updated (added `no_acme`).
* [x] Helm chart full values reference defined.

---

## P1 — Full Helm Chart

Goal: chart covers every feature in the README, even before the features are implemented in Go. Values fields that map to unimplemented features are scaffolded and no-op at runtime until the feature lands.

* [ ] **Deployment modes**: `mode: library | sidecar` toggle. Sidecar mode renders two containers.
* [ ] **Sidecar app container**: `sidecar.app.image`, `sidecar.app.port`, lifecycle hooks.
* [ ] **Secrets mount**: `keel.secrets.existingSecret` mounts a k8s Secret as a file; sets `KEEL_SECRETS` env var.
* [ ] **TLS cert secret**: `keel.tls.certSecretName` mounts `tls.crt` / `tls.key` from a k8s TLS Secret.
* [ ] **ACME PVC**: `keel.tls.acme.cachePVC` mounts a PVC at `/var/lib/keel/acme` for cert storage.
* [ ] **Startup probe**: `/startupz` on `listeners.startup.port`.
* [ ] **Admin port**: `listeners.admin.enabled` renders admin containerPort and optional Service.
* [ ] **ServiceMonitor**: `serviceMonitor.enabled` creates a Prometheus Operator `ServiceMonitor` CR.
* [ ] **NetworkPolicy**: `networkPolicy.enabled` creates a `NetworkPolicy` (kubelet CIDR for probes, cluster for main, configurable for admin).
* [ ] **PodDisruptionBudget**: `podDisruptionBudget.enabled` creates a PDB.
* [ ] **Pre-stop hook**: `terminationGracePeriodSeconds` and `lifecycle.preStop` rendered from `keel.timeouts.prestopSleep`.
* [ ] **OTLP / StatsD / Prometheus values**: all metrics/tracing fields scaffolded in values.yaml.
* [ ] **All listener ports**: health, ready, startup, admin named in containerPorts.
* [ ] **Helm lint CI gate**: `helm lint` and `helm template | kubeval` in CI pipeline.
* [ ] **Helm test**: `helm test` pod that hits `/healthz` and `/version`.

---

## P2 — YAML Config + Secrets

Goal: replace JSON config with YAML. All fields from the README §3.8 schema are loadable.

* [ ] **Switch config parser to YAML** (`gopkg.in/yaml.v3`). Remove JSON config parsing.
* [ ] **Full schema**: all fields from keel.yaml schema in README implemented in `Config` struct.
* [ ] **`TrustedIDs` / `TrustedSigners` ENV vars**: `KEEL_TRUSTED_IDS` (comma-separated), `KEEL_TRUSTED_SIGNERS` (comma-separated).
* [ ] **`MySignatureKey`**: add `my_signature_key_file` to config; load private key from path.
* [ ] **Secrets file**: load `keel-secrets.yaml` from `KEEL_SECRETS` path; merge over primary config.
* [ ] **`*_file` key convention**: values ending in `_file` are read from disk; content replaces the field value.
* [ ] **Merge order**: defaults → keel.yaml → keel-secrets.yaml → ENV vars.
* [ ] **Config validation at startup**: fail fast with clear error on invalid combos (see README §3.8.4).
* [ ] **`--validate` flag**: check config and exit 0/1 without starting listeners.
* [ ] **Fix `mergeListener.Enabled` bug**: distinguish "not set in file" from `false`; use `*bool` or explicit presence map.
* [ ] **Tests (TDD — write failing tests first)**:
  * YAML file loads all scalar fields correctly.
  * Secrets file overrides YAML file values.
  * ENV vars override secrets file values.
  * Partial file does not clobber ENV-set listener `Enabled`.
  * `KEEL_TRUSTED_IDS=alice,bob` populates `[]string{"alice","bob"}`.
  * Invalid config (HTTPS + no cert + no ACME) → `LoadConfig` returns error.
  * `--validate` exits 0 on good config, 1 on bad config.

---

## P3 — Docker Compose Integration Test Harness

Goal: all integration tests run against the named compose topology from the README. No more ad-hoc port selection.

* [ ] **`docker-compose.test.yaml`**: services as defined in README §9 (upstream, keel, prometheus, otel-collector, jaeger).
* [ ] **Test fixtures**: `tests/fixtures/keel.yaml`, `tests/fixtures/secrets/keel-secrets.yaml`, `tests/fixtures/certs/` (self-signed for tests), `tests/fixtures/prometheus.yml`, `tests/fixtures/otel-collector.yaml`.
* [ ] **`scripts/ci_test.sh`**: orchestrates `docker compose up -d`, waits for health, runs `go test ./tests/integration/...`, tears down.
* [ ] **Named ports**: all ports fixed and named in compose file; test code references by name from a shared constants file.
* [ ] **Migrate existing integration tests** to use compose topology (no more ad-hoc listeners on random ports).
* [ ] **Sidecar integration test**: compose variant with app container + keel sidecar; tests proxying, header forwarding, upstream-down → 503 behavior.
* [ ] **TLS integration test**: compose with self-signed cert; tests HTTPS listener end-to-end.

---

## P3.5 — Local k8s via macOS Colima

Goal: a one-command local Kubernetes environment for testing the Helm chart end-to-end before pushing to staging.

* [ ] **`scripts/colima/setup.sh`**: install Colima + kubectl + helm via Homebrew; start a Colima VM with `--kubernetes --runtime docker`; wait for all nodes Ready.
* [ ] **`scripts/colima/deploy.sh`**: build `keel:test` image inside the Colima VM via `colima nerdctl`; `helm upgrade --install` using `tests/fixtures/colima/values.yaml`.
* [ ] **`scripts/colima/test.sh`**: k8s-specific test scenarios:
  * Pod reaches `Ready` condition within 120s.
  * `/healthz` and `/readyz` endpoints respond via `kubectl port-forward`.
  * Rolling restart (`kubectl rollout restart`) completes without error.
* [ ] **`scripts/colima/teardown.sh`**: `helm uninstall` + `colima stop`.
* [ ] **`tests/fixtures/colima/values.yaml`**: minimal Helm overrides for local testing (single replica, `imagePullPolicy: Never`, HTTP only, authn disabled).
* [ ] **Makefile targets**: `colima-setup`, `colima-deploy`, `colima-test`, `colima-teardown`.

---

## P4 — TLS Correctness

* [ ] **TLS 1.3 only**: fix `BuildTLSConfig` — `MinVersion: tls.VersionTLS13`, no `MaxVersion` ceiling.
* [ ] **Remove `PreferServerCipherSuites`**: deprecated and meaningless in TLS 1.3.
* [ ] **FIPS build**: `policy_fips.go` targets BoringCrypto TLS 1.3 cipher sets only.
* [ ] **`no_fips` build**: standard Go TLS with TLS 1.3 min.
* [ ] **Tests (TDD)**:
  * `BuildTLSConfig` returns `MinVersion == tls.VersionTLS13`.
  * TLS 1.2 connection attempt is rejected.
  * FIPS build does not offer non-FIPS cipher suites.
  * `keel_tls_cert_expiry_seconds` gauge set correctly from loaded cert.

---

## P5 — Core Middleware Correctness

* [ ] **OWASP**: add missing `strict-transport-security` header (HTTPS listeners only); verify all 6 headers in tests.
* [ ] **OWASP**: enforce `max_header_bytes` via `http.Server.MaxHeaderBytes`.
* [ ] **OWASP**: enforce `max_response_body_bytes` (response body reader wrapper).
* [ ] **Authn**: support RS256 and ES256 in addition to HS256.
* [ ] **Authn**: try all `trusted_signers` entries, not just `[0]`.
* [ ] **Authn**: support JWKs URL entries in `trusted_signers` (fetch + cache with TTL).
* [ ] **Authn mTLS**: client cert CN/SAN → principal ID mapping; same `trusted_ids` check.
* [ ] **Shedding**: return 429 (not 503) when queue is full; return 503 when not ready due to backpressure.
* [ ] **Tests (TDD — failing tests first)**:
  * All 6 OWASP security headers present on every response.
  * HSTS header absent on plain HTTP response; present on HTTPS response.
  * Body > limit → 413.
  * Expired JWT → 401.
  * Wrong signer → 401.
  * Forbidden sub → 403.
  * Missing `Authorization` header → 401.
  * RS256 / ES256 JWT accepted.
  * Second trusted signer accepted when first fails.
  * Pressure high → not-ready → new request gets 503; pressure drops → 200.

---

## P6 — Prometheus Metrics

* [ ] **`/metrics` endpoint**: Prometheus text format on admin port (or main if admin disabled).
* [ ] **Implement all metrics from README §4.3 table**.
* [ ] **Middleware instrumentation**: wrap main handler to record RED metrics + inflight gauge.
* [ ] **`keel_fips_active` gauge**.
* [ ] **`keel_tls_cert_expiry_seconds` gauge** (updated hourly).
* [ ] **`no_prom` build tag**: removes all Prometheus code and dependency.
* [ ] **Tests (TDD)**:
  * `/metrics` returns 200 with correct content type.
  * Request completes → `keel_requests_total` incremented with correct labels.
  * Inflight gauge increments during request, decrements after.
  * `no_prom` build: `/metrics` returns 404.
  * `keel_fips_active` is 1 in FIPS build, 0 otherwise.

---

## P7 — Access Log + Request Correlation

* [ ] **`X-Request-ID` middleware**: read inbound header; generate ULID if absent; set on response; propagate to upstream (sidecar).
* [ ] **Per-request access log line**: all fields from README §4.5 schema.
* [ ] **Logger context**: `trace_id`, `span_id`, `request_id` available to all log calls within a request.
* [ ] **`ctxkeys` package**: export `RequestID`, `TraceID`, `SpanID` context keys for app use.
* [ ] **`mw` package export**: `mw.RequestID`, `mw.AccessLog` composable and exported.
* [ ] **Tests (TDD)**:
  * Inbound `X-Request-ID` is preserved and echoed.
  * Missing `X-Request-ID` gets generated ULID.
  * Access log line contains all required fields.
  * `ctxkeys.RequestID` is set in handler context.

---

## P8 — OTLP Tracing

* [ ] **OTLP span per request**: start, end, set attributes from README §4.2.
* [ ] **W3C Trace Context propagation**: read/write `traceparent` / `tracestate`.
* [ ] **OTLP gRPC export** to configurable endpoint.
* [ ] **`no_otel` build tag**: removes all OTel dependencies.
* [ ] **Tests (TDD)**:
  * Inbound `traceparent` is propagated to upstream (sidecar mode).
  * Missing `traceparent` generates a new trace ID.
  * `no_otel` build compiles cleanly with no OTel imports.

---

## P9 — Concurrency Limits + Queue

* [ ] **Semaphore middleware**: cap in-flight requests at `limits.max_concurrent`; return 429 when full (no queue) or when queue overflows.
* [ ] **Request queue**: hold up to `limits.queue_depth` requests; dequeue when semaphore slot opens; reject with 429 if timeout exceeded while queued.
* [ ] **Tests (TDD)**:
  * `max_concurrent=2`: third concurrent request gets 429.
  * `queue_depth=5`: sixth concurrent request gets 429; first five eventually complete.
  * Queued request exceeding write timeout gets 503.

---

## P10 — Sidecar Advanced Behaviors

* [ ] **Upstream health probing**: background goroutine probing `upstream_health_path` at `upstream_health_interval`.
* [ ] **Upstream health → readiness**: flip `/readyz` when upstream unhealthy; restore when recovered.
* [ ] **Circuit breaker**: state machine (closed → open → half-open) as specified in README §8.2.
* [ ] **XFF policy**: `xff_mode: append | replace | strip`; `xff_trusted_hops` right-stripping.
* [ ] **`X-Real-IP`**: set to trusted client IP.
* [ ] **Header forwarding policy**: `forward` allowlist and `strip` denylist.
* [ ] **Outbound JWT signing**: if `my_signature_key_file` set, sign outbound requests with `my_id` as `sub`.
* [ ] **Response size cap**: truncate + 502 if upstream response exceeds `max_response_body_bytes`.
* [ ] **Upstream TLS (out-of-pod)**: when `upstream_tls.enabled`, dial upstream over TLS.
  * Server cert verification against system roots or pinned `ca_file`.
  * mTLS: load and present `client_cert_file` + `client_key_file` at handshake.
  * `insecure_skip_verify` wired but blocked by config validation in production builds (warn loudly, require explicit opt-in).
  * TLS 1.3 only for upstream connections (same policy as inbound).
  * Helm chart: `sidecar.upstreamTLSSecret` mounts a k8s Secret as `ca.crt`, `tls.crt`, `tls.key` at `/etc/keel/secrets/upstream-tls/`; secrets file wires the paths automatically.
* [ ] **Tests (TDD)**:
  * Intra-pod (localhost, no TLS): proxy works, no TLS dial attempted.
  * Out-of-pod (https upstream): TLS dial made; bad server cert → 502.
  * mTLS: upstream rejects client with wrong cert → 502; correct cert → 200.
  * `insecure_skip_verify: true` + self-signed upstream → 200 (test only).
  * upstream-down → `/readyz` 503; upstream-recovered → `/readyz` 200.
  * Circuit opens after `failure_threshold`; half-open probe succeeds → closes.
  * XFF modes: append, replace, strip.
  * Response size cap: upstream over limit → 502.

---

## P11 — SIGHUP Hot Reload

* [ ] **SIGHUP handler**: re-read config + secrets; validate; apply live fields; keep running on invalid config.
* [ ] **TLS cert reload**: new cert loaded; new handshakes use new cert; in-flight sessions unaffected.
* [ ] **`POST /admin/reload`**: HTTP equivalent on admin port.
* [ ] **`SIGUSR1`**: dump current config to stderr (diagnostic).
* [ ] **`SIGUSR2`**: rotate access log file handle.
* [ ] **Tests (TDD)**:
  * SIGHUP with valid config → config updated, no downtime.
  * SIGHUP with invalid config → error logged, old config active.
  * `/admin/reload` returns 200 on success, 422 on invalid config.

---

## P12 — ACME / Let's Encrypt

* [ ] **ACME client**: integrate `golang.org/x/crypto/acme` or `github.com/caddyserver/certmagic`.
* [ ] **http-01 challenge route**: `GET /.well-known/acme-challenge/<token>` registered on HTTP listener before authn/redirect middleware.
* [ ] **HTTP → HTTPS redirect**: all other HTTP paths 301-redirected to HTTPS.
* [ ] **Cert cache**: disk-based at `tls.acme.cache_dir`; compatible with a k8s PVC.
* [ ] **Auto-renewal**: background renewal before expiry (30-day lead); zero-downtime cert swap.
* [ ] **`no_acme` build tag**: removes ACME client and dependency.
* [ ] **Tests (TDD)**:
  * Challenge route accessible over HTTP while other paths redirect.
  * Challenge route is not behind authn middleware.
  * `no_acme` build: ACME config fields ignored / error if set.

---

## P13 — StatsD

* [ ] **StatsD output**: counters/gauges/timers aligned with Prometheus metric names.
* [ ] **UDP emit**: configurable endpoint and prefix.
* [ ] **`no_statsd` build tag**: removes StatsD code.
* [ ] **Tests (TDD)**: mock UDP listener; assert metric emitted on request completion.

---

## P14 — Remote Log Sink

* [ ] **HTTP sink**: POST structured log batches to configurable endpoint.
* [ ] **Syslog/TCP sink**: RFC 5424 syslog output.
* [ ] **Buffered + non-blocking**: drop on overflow; `keel_log_drops_total` counter.
* [ ] **`no_remotelog` build tag**: removes remote sink code.

---

## P15 — Operational Endpoints

* [ ] **`GET /version`**: JSON response as per README §4.6.
* [ ] **`GET /health/fips`**: JSON `{"fips_active": true/false}`.
* [ ] **`GET /debug/pprof/`**: Go pprof endpoints on admin port only.
* [ ] **`GET /startupz`**: startup probe; 503 until initialization complete.
* [ ] **Readiness dependency registration**: `keel.WithReadinessCheck(name, fn)` option; all checks run on `/readyz`.
* [ ] **Tests (TDD)**:
  * `/version` returns correct version + build tags.
  * `/startupz` returns 503 before startup complete, 200 after.
  * Failed readiness check → `/readyz` 503 with check name in body.
  * `/debug/pprof/` returns 404 on main port; 200 on admin port.

---

## P16 — FIPS

* [ ] **`/health/fips` endpoint** (part of P15).
* [ ] **`keel_fips_active` gauge** (part of P6).
* [ ] **FIPS gatekeeper test**: BATS test that runs `max` (FIPS) build in non-FIPS environment and asserts non-zero exit code.
* [ ] **FIPS Compatibility Mode**: document BoringCrypto-backed build as FIPS-compatible (short-term).
* [ ] **Formal FIPS 140-2/3 certification**: pursue for binary distribution (long-term).

---

## P17 — Binary Size + CI Gates

* [ ] **Binary size check**: BATS/CI script fails if `min` build exceeds 4 MB.
* [ ] **BATS suite**: `tests/integrity.bats` verifies CLI flags, `--validate`, `--version`, `SIGHUP`, `SIGTERM`.
* [ ] **Signal testing**: BATS verifies `SIGTERM` graceful shutdown; `SIGHUP` reload.
* [ ] **Windows shutdown integration test**: CI Windows runner tests console event shutdown.

---

## P19 — GitHub Actions CI Workflows

Goal: every CI gate is a thin workflow file that delegates to an existing `scripts/` entrypoint. No build logic lives in YAML — workflows are pure orchestration (checkout → setup-go → call script).

* [ ] **`ci.yml`** — triggered on push and pull_request to `main`:
  * **unit** job (ubuntu-latest, macos-latest, windows-latest matrix): `make test-unit` → `scripts/test/ci.sh`.
  * **build-min** job (ubuntu-latest): `scripts/build/ci_min.sh` (produces `dist/keel-min`).
  * **integrity** job (ubuntu-latest, depends on build-min): install `bats-core`; run `bats tests/integrity.bats`.
  * **build-max-no-fips** job (ubuntu-latest, macos-latest, windows-latest matrix): `scripts/build/ci_max_no_fips.sh`.
  * **build-max-fips** job (ubuntu-latest): `scripts/build/ci_max.sh` (FIPS + symbol verification).
  * **lint** job (ubuntu-latest): `go vet ./...` + `staticcheck ./...` (or `golangci-lint`).
* [ ] **`release.yml`** — triggered on `v*` tag push:
  * Build `keel-min`, `keel-max`, `keel-fips` on ubuntu-latest using their respective `scripts/build/ci_*.sh` scripts.
  * Upload binaries as GitHub Release assets via `gh release upload`.
  * Minimal YAML: checkout → setup-go → call script → upload artifact.
* [ ] **`helm-lint.yml`** — triggered on changes to `charts/**`:
  * `helm lint charts/keel` and `helm template charts/keel | kubectl apply --dry-run=client -f -`.
* [ ] **Workflow conventions** (enforced by PR review):
  * All Go logic stays in `scripts/`; workflow YAML contains no `go build` / `go test` invocations directly.
  * `timeout-minutes` set on every job (unit: 10, build: 15, integrity: 5, FIPS: 20).
  * `permissions: contents: read` (least privilege) on all workflows; `release.yml` adds `contents: write` for asset upload only.
  * Pinned action versions (e.g. `actions/checkout@v4`, `actions/setup-go@v5`).
  * Go version read from `go.mod` via `go-version-file: go.mod` — single source of truth.
* [ ] **Windows smoke test** (windows-latest runner in `ci.yml` build-max-no-fips job):
  * After `ci_max_no_fips.sh` (or PowerShell equivalent), run `dist\keel-max.exe --version`.
  * Confirms Windows binary starts and prints version; console-event shutdown tested here.
* [ ] **macOS integrity** (macos-latest runner):
  * `scripts/build/ci_min.sh` + `bats tests/integrity.bats` (size gate auto-skips on Darwin; signal tests run).
* [ ] **Caching**: `actions/cache` for Go module cache and build cache keyed on `go.sum` hash.
* [ ] **Tests (validation)**:
  * Each new workflow passes a dry-run (`act` or manual PR) before merge.
  * `helm-lint.yml` fails if chart template renders invalid YAML.
  * Release workflow produces correctly named assets (`keel-min-linux-amd64`, etc.) with `GOOS`/`GOARCH` in filename.

---

## P18 — GTM / DX (Post-Core)

* [ ] **"Keel-Haul" CLI**: standalone TUI companion to wrap local processes with Keel's security posture.
* [ ] **"Verified Secure" Badge Service**: generate a scorecard/badge from active build tags.
* [ ] **Build-a-Binary Web UI**: web configurator for generating `go build` commands.
* [ ] **Build-time embedded default cert**: embed one cert at build time for scratch images (dev/test).
* [ ] **OIDC/OAuth2 Proxying**: full OIDC redirect flows for Okta, Azure AD, etc.
* [ ] **Immutable Audit Logging**: dedicated log stream for security events (Authn, 403s, config reloads).
* [ ] **WASM Middleware**: extension points for WASM-based handlers.
* [ ] **Logo Acquisition**: finalize "Anchor" visual identity.
* [ ] **TRADEMARK.md**: periodic review for ecosystem usage.
* [ ] **`--check-integrity` flag**: print Trademark & License notice.
* [ ] **`--check-shred` flag**: verify binary size and FIPS compliance.
* [ ] **Automatic SBOM**: embed SBOM in binary.
