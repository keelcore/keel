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

* [x] **Deployment modes**: `mode: library | sidecar` toggle. Sidecar mode renders two containers.
* [x] **Sidecar app container**: `sidecar.app.image`, `sidecar.app.port`, lifecycle hooks.
* [x] **Secrets mount**: `keel.secrets.existingSecret` mounts a k8s Secret as a file; sets `KEEL_SECRETS` env var.
* [x] **TLS cert secret**: `keel.tls.certSecretName` mounts `tls.crt` / `tls.key` from a k8s TLS Secret.
* [x] **ACME PVC**: `keel.tls.acme.cachePVC` mounts a PVC at `/var/lib/keel/acme` for cert storage.
* [x] **Startup probe**: `/startupz` on `listeners.startup.port`.
* [x] **Admin port**: `listeners.admin.enabled` renders admin containerPort and optional Service.
* [x] **ServiceMonitor**: `serviceMonitor.enabled` creates a Prometheus Operator `ServiceMonitor` CR.
* [x] **NetworkPolicy**: `networkPolicy.enabled` creates a `NetworkPolicy` (kubelet CIDR for probes, cluster for main, configurable for admin).
* [x] **PodDisruptionBudget**: `podDisruptionBudget.enabled` creates a PDB.
* [x] **Pre-stop hook**: `terminationGracePeriodSeconds` and `lifecycle.preStop` rendered from `keel.timeouts.prestopSleep`.
* [x] **OTLP / StatsD / Prometheus values**: all metrics/tracing fields scaffolded in values.yaml.
* [x] **All listener ports**: health, ready, startup, admin named in containerPorts.
* [x] **Helm lint CI gate**: `helm lint` and `helm template | kubeval` in CI pipeline.
* [x] **Helm test**: `helm test` pod that hits `/healthz` and `/version`.

---

## P2 — YAML Config + Secrets

Goal: replace JSON config with YAML. All fields from the README §3.8 schema are loadable.

* [x] **Switch config parser to YAML** (`gopkg.in/yaml.v3`). Remove JSON config parsing.
* [x] **Full schema**: all fields from keel.yaml schema in README implemented in `Config` struct.
* [x] **`TrustedIDs` / `TrustedSigners` ENV vars**: `KEEL_TRUSTED_IDS` (comma-separated), `KEEL_TRUSTED_SIGNERS` (comma-separated).
* [x] **`MySignatureKey`**: add `my_signature_key_file` to config; load private key from path.
* [x] **Secrets file**: load `keel-secrets.yaml` from `KEEL_SECRETS` path; merge over primary config.
* [x] **`*_file` key convention**: values ending in `_file` are read from disk; content replaces the field value.
* [x] **Merge order**: defaults → keel.yaml → keel-secrets.yaml → ENV vars.
* [x] **Config validation at startup**: fail fast with clear error on invalid combos (see README §3.8.4).
* [x] **`--validate` flag**: check config and exit 0/1 without starting listeners.
* [x] **Fix `mergeListener.Enabled` bug**: distinguish "not set in file" from `false`; use `*bool` or explicit presence map.
* [x] **Tests (TDD — write failing tests first)**:
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

* [x] **`docker-compose.test.yaml`**: services as defined in README §9 (upstream, keel, prometheus, otel-collector, jaeger).
* [x] **Test fixtures**: `tests/fixtures/keel.yaml`, `tests/fixtures/secrets/keel-secrets.yaml`, `tests/fixtures/certs/` (self-signed for tests), `tests/fixtures/prometheus.yml`, `tests/fixtures/otel-collector.yaml`.
* [x] **`scripts/ci_test.sh`**: orchestrates `docker compose up -d`, waits for health, runs `go test ./tests/integration/...`, tears down.
* [x] **Named ports**: all ports fixed and named in compose file; test code references by name from a shared constants file.
* [x] **Migrate existing integration tests** to use compose topology (no more ad-hoc listeners on random ports).
* [x] **Sidecar integration test**: compose variant with app container + keel sidecar; tests proxying, header forwarding, upstream-down → 503 behavior.
* [x] **TLS integration test**: compose with self-signed cert; tests HTTPS listener end-to-end.

---

## P3.5 — Local k8s via macOS Colima

Goal: a one-command local Kubernetes environment for testing the Helm chart end-to-end before pushing to staging.

* [x] **`scripts/colima/setup.sh`**: install Colima + kubectl + helm via Homebrew; start a Colima VM with `--kubernetes --runtime docker`; wait for all nodes Ready.
* [x] **`scripts/colima/deploy.sh`**: build `keel:test` image inside the Colima VM via `colima nerdctl`; `helm upgrade --install` using `tests/fixtures/colima/values.yaml`.
* [x] **`scripts/colima/test.sh`**: k8s-specific test scenarios:
  * Pod reaches `Ready` condition within 120s.
  * `/healthz` and `/readyz` endpoints respond via `kubectl port-forward`.
  * Rolling restart (`kubectl rollout restart`) completes without error.
* [x] **`scripts/colima/teardown.sh`**: `helm uninstall` + `colima stop`.
* [x] **`tests/fixtures/colima/values.yaml`**: minimal Helm overrides for local testing (single replica, `imagePullPolicy: Never`, HTTP only, authn disabled).
* [x] **Makefile targets**: `colima-setup`, `colima-deploy`, `colima-test`, `colima-teardown`.

---

## P4 — TLS Correctness

* [x] **TLS 1.3 only**: fix `BuildTLSConfig` — `MinVersion: tls.VersionTLS13`, no `MaxVersion` ceiling.
* [x] **Remove `PreferServerCipherSuites`**: deprecated and meaningless in TLS 1.3.
* [x] **FIPS build**: `policy_fips.go` targets BoringCrypto TLS 1.3 cipher sets only.
* [x] **`no_fips` build**: standard Go TLS with TLS 1.3 min.
* [x] **Tests (TDD)**:
  * `BuildTLSConfig` returns `MinVersion == tls.VersionTLS13`.
  * TLS 1.2 connection attempt is rejected.
  * FIPS build does not offer non-FIPS cipher suites.
  * `keel_tls_cert_expiry_seconds` gauge set correctly from loaded cert.

---

## P5 — Core Middleware Correctness

* [x] **OWASP**: add missing `strict-transport-security` header (HTTPS listeners only); verify all 6 headers in tests.
* [x] **OWASP**: enforce `max_header_bytes` via `http.Server.MaxHeaderBytes`.
* [x] **OWASP**: enforce `max_response_body_bytes` (response body reader wrapper).
* [x] **Authn**: support RS256 and ES256 in addition to HS256.
* [x] **Authn**: try all `trusted_signers` entries, not just `[0]`.
* [x] **Authn**: support JWKs URL entries in `trusted_signers` (fetch + cache with TTL).
* [x] **Authn mTLS**: client cert CN/SAN → principal ID mapping; same `trusted_ids` check.
* [x] **Shedding**: return 429 (not 503) when queue is full; return 503 when not ready due to backpressure.
* [x] **Tests (TDD — failing tests first)**:
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

* [x] **`/metrics` endpoint**: Prometheus text format on admin port (or main if admin disabled).
* [x] **Implement all metrics from README §4.3 table**.
* [x] **Middleware instrumentation**: wrap main handler to record RED metrics + inflight gauge.
* [x] **`keel_fips_active` gauge**.
* [x] **`keel_tls_cert_expiry_seconds` gauge** (updated hourly).
* [x] **`no_prom` build tag**: removes all Prometheus code and dependency.
* [x] **Tests (TDD)**:
  * `/metrics` returns 200 with correct content type.
  * Request completes → `keel_requests_total` incremented with correct labels.
  * Inflight gauge increments during request, decrements after.
  * `no_prom` build: `/metrics` returns 404.
  * `keel_fips_active` is 1 in FIPS build, 0 otherwise.

---

## P7 — Access Log + Request Correlation

* [x] **`X-Request-ID` middleware**: read inbound header; generate ULID if absent; set on response; propagate to upstream (sidecar).
* [x] **Per-request access log line**: all fields from README §4.5 schema.
* [x] **Logger context**: `trace_id`, `span_id`, `request_id` available to all log calls within a request.
* [x] **`ctxkeys` package**: export `RequestID`, `TraceID`, `SpanID` context keys for app use.
* [x] **`mw` package export**: `mw.RequestID`, `mw.AccessLog` composable and exported.
* [x] **Tests (TDD)**:
  * Inbound `X-Request-ID` is preserved and echoed.
  * Missing `X-Request-ID` gets generated ULID.
  * Access log line contains all required fields.
  * `ctxkeys.RequestID` is set in handler context.

---

## P8 — OTLP Tracing

* [x] **OTLP span per request**: start, end, set attributes from README §4.2.
* [x] **W3C Trace Context propagation**: read/write `traceparent` / `tracestate`.
* [x] **OTLP gRPC export** to configurable endpoint.
* [x] **`no_otel` build tag**: removes all OTel dependencies.
* [x] **Tests (TDD)**:
  * Inbound `traceparent` is propagated to upstream (sidecar mode).
  * Missing `traceparent` generates a new trace ID.
  * `no_otel` build compiles cleanly with no OTel imports.

---

## P9 — Concurrency Limits + Queue

* [x] **Semaphore middleware**: cap in-flight requests at `limits.max_concurrent`; return 429 when full (no queue) or when queue overflows.
* [x] **Request queue**: hold up to `limits.queue_depth` requests; dequeue when semaphore slot opens; reject with 429 if timeout exceeded while queued.
* [x] **Tests (TDD)**:
  * `max_concurrent=2`: third concurrent request gets 429.
  * `queue_depth=5`: sixth concurrent request gets 429; first five eventually complete.
  * Queued request exceeding write timeout gets 503.

---

## P10 — Sidecar Advanced Behaviors

* [x] **Upstream health probing**: background goroutine probing `upstream_health_path` at `upstream_health_interval`.
* [x] **Upstream health → readiness**: flip `/readyz` when upstream unhealthy; restore when recovered.
* [x] **Circuit breaker**: state machine (closed → open → half-open) as specified in README §8.2.
* [x] **XFF policy**: `xff_mode: append | replace | strip`; `xff_trusted_hops` right-stripping.
* [x] **`X-Real-IP`**: set to trusted client IP.
* [x] **Header forwarding policy**: `forward` allowlist and `strip` denylist.
* [x] **Outbound JWT signing**: if `my_signature_key_file` set, sign outbound requests with `my_id` as `sub`.
* [x] **Response size cap**: truncate + 502 if upstream response exceeds `max_response_body_bytes`.
* [x] **Upstream TLS (out-of-pod)**: when `upstream_tls.enabled`, dial upstream over TLS.
  * Server cert verification against system roots or pinned `ca_file`.
  * mTLS: load and present `client_cert_file` + `client_key_file` at handshake.
  * `insecure_skip_verify` wired but blocked by config validation in production builds (warn loudly, require explicit opt-in).
  * TLS 1.3 only for upstream connections (same policy as inbound).
  * Helm chart: `sidecar.upstreamTLSSecret` mounts a k8s Secret as `ca.crt`, `tls.crt`, `tls.key` at `/etc/keel/secrets/upstream-tls/`; secrets file wires the paths automatically.
* [x] **Tests (TDD)**:
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

* [x] **SIGHUP handler**: re-read config + secrets; validate; apply live fields; keep running on invalid config.
* [x] **TLS cert reload**: new cert loaded; new handshakes use new cert; in-flight sessions unaffected.
* [x] **`POST /admin/reload`**: HTTP equivalent on admin port.
* [x] **`SIGUSR1`**: dump current config to stderr (diagnostic).
* [x] **`SIGUSR2`**: rotate access log file handle.
* [x] **Tests (TDD)**:
  * SIGHUP with valid config → config updated, no downtime.
  * SIGHUP with invalid config → error logged, old config active.
  * `/admin/reload` returns 200 on success, 422 on invalid config.

---

## P12 — ACME / Let's Encrypt

* [x] **ACME client**: integrate `golang.org/x/crypto/acme` or `github.com/caddyserver/certmagic`.
* [x] **http-01 challenge route**: `GET /.well-known/acme-challenge/<token>` registered on HTTP listener before authn/redirect middleware.
* [x] **HTTP → HTTPS redirect**: all other HTTP paths 301-redirected to HTTPS.
* [x] **Cert cache**: disk-based at `tls.acme.cache_dir`; compatible with a k8s PVC.
* [x] **Auto-renewal**: background renewal before expiry (30-day lead); zero-downtime cert swap.
* [x] **`no_acme` build tag**: removes ACME client and dependency.
* [x] **Tests (TDD)**:
  * Challenge route accessible over HTTP while other paths redirect.
  * Challenge route is not behind authn middleware.
  * `no_acme` build: ACME config fields ignored / error if set.

---

## P13 — StatsD

* [x] **StatsD output**: counters/gauges/timers aligned with Prometheus metric names.
* [x] **UDP emit**: configurable endpoint and prefix.
* [x] **`no_statsd` build tag**: removes StatsD code.
* [x] **Tests (TDD)**: mock UDP listener; assert metric emitted on request completion.

---

## P14 — Remote Log Sink

* [x] **HTTP sink**: POST structured log batches to configurable endpoint.
* [x] **Syslog/TCP sink**: RFC 5424 syslog output.
* [x] **Buffered + non-blocking**: drop on overflow; `keel_log_drops_total` counter.
* [x] **`no_remotelog` build tag**: removes remote sink code.

---

## P15 — Operational Endpoints

* [x] **`GET /version`**: JSON response as per README §4.6.
* [x] **`GET /health/fips`**: JSON `{"fips_active": true/false}`.
* [x] **`GET /debug/pprof/`**: Go pprof endpoints on admin port only.
* [x] **`GET /startupz`**: startup probe; 503 until initialization complete.
* [x] **Readiness dependency registration**: `keel.WithReadinessCheck(name, fn)` option; all checks run on `/readyz`.
* [x] **Tests (TDD)**:
  * `/version` returns correct version + build tags.
  * `/startupz` returns 503 before startup complete, 200 after.
  * Failed readiness check → `/readyz` 503 with check name in body.
  * `/debug/pprof/` returns 404 on main port; 200 on admin port.

---

## P16 — FIPS

* [x] **`/health/fips` endpoint** (part of P15).
* [x] **`keel_fips_active` gauge** (part of P6).
* [x] **FIPS gatekeeper test**: BATS test that runs `max` (FIPS) build in non-FIPS environment and asserts non-zero exit code.
* [x] **FIPS Compatibility Mode**: document BoringCrypto-backed build as FIPS-compatible (short-term).
* [x] **Formal FIPS 140-2/3 certification**: pursue for binary distribution (long-term).

---

## P17 — Binary Size + CI Gates

* [x] **Binary size check**: BATS/CI script fails if `min` build exceeds 4 MB.
* [x] **BATS suite**: `tests/integrity.bats` verifies CLI flags, `--validate`, `--version`, `SIGHUP`, `SIGTERM`.
* [x] **Signal testing**: BATS verifies `SIGTERM` graceful shutdown; `SIGHUP` reload.
* [x] **Windows shutdown integration test**: CI Windows runner tests console event shutdown.

---

## P19 — GitHub Actions CI Workflows

Goal: every CI gate is a thin workflow file that delegates to an existing `scripts/` entrypoint. No build logic lives in YAML — workflows are pure orchestration (checkout → setup-go → call script).

* [x] **`ci.yml`** — triggered on push and pull_request to `main`:
  * **unit** job (ubuntu-latest, macos-latest, windows-latest matrix): `make test-unit` → `scripts/test/ci.sh`.
  * **build-min** job (ubuntu-latest): `scripts/build/ci_min.sh` (produces `dist/keel-min`).
  * **integrity** job (ubuntu-latest, depends on build-min): install `bats-core`; run `bats tests/integrity.bats`.
  * **build-max-no-fips** job (ubuntu-latest, macos-latest, windows-latest matrix): `scripts/build/ci_max_no_fips.sh`.
  * **build-max-fips** job (ubuntu-latest): `scripts/build/ci_max.sh` (FIPS + symbol verification).
  * **lint** job (ubuntu-latest): `go vet ./...` + `staticcheck ./...` (or `golangci-lint`).
* [x] **`release.yml`** — triggered on `v*` tag push:
  * Build `keel-min`, `keel-max`, `keel-fips` on ubuntu-latest using their respective `scripts/build/ci_*.sh` scripts.
  * Upload binaries as GitHub Release assets via `gh release upload`.
  * Minimal YAML: checkout → setup-go → call script → upload artifact.
* [x] **`helm-lint.yml`** — triggered on changes to `helm/**`:
  * `helm lint helm/keel` and `helm template helm/keel | kubectl apply --dry-run=client -f -`.
* [x] **Workflow conventions** (enforced by PR review):
  * All Go logic stays in `scripts/`; workflow YAML contains no `go build` / `go test` invocations directly.
  * `timeout-minutes` set on every job (unit: 10, build: 15, integrity: 5, FIPS: 20).
  * `permissions: contents: read` (least privilege) on all workflows; `release.yml` adds `contents: write` for asset upload only.
  * Pinned action versions with full commit SHAs.
  * Go version read from `go.mod` via `go-version-file: go.mod` — single source of truth.
* [x] **Windows smoke test** (windows-latest runner via `build-windows-smoke` job):
  * `scripts/build/ci_smoke_windows.sh` builds via `ci_max_no_fips.sh`, renames to `.exe`, runs `--version`.
  * Confirms Windows binary starts and exits cleanly.
* [x] **macOS integrity** (macos-latest runner via `integrity-macos` job):
  * `scripts/build/ci_min.sh` + `scripts/ci/setup-bats.sh` (now supports brew) + `bats tests/integrity.bats`.
  * Binary size gate auto-skips on Darwin; signal tests run.
* [x] **Caching**: `cache: true` in `actions/setup-go` covers Go module cache and build cache.
* [x] **Tests (validation)**:
  * `helm-lint.yml` fails if chart template renders invalid YAML.
  * Release workflow produces correctly named assets (`keel-min-linux-amd64`, etc.) via `scripts/release/rename-assets.sh` called before checksum generation.

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