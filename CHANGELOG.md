# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Security vulnerabilities are reported via [GitHub Security Advisories](https://github.com/keelcore/keel/security/advisories).
Each release identifies all CVE fixes per [OpenSSF best practices](https://bestpractices.coreinfrastructure.org/projects/12121).

## [Unreleased]

### Changed

- New `no_pprof` build tag drops the profiling handlers (`net/http/pprof`, `runtime/pprof`); the `min`
  flavor uses it, bringing linux/amd64 keel-min under the 8 MB gate (8.25 MB → was 8.88 MB). pprof was
  already off by default (`admin.pprof: false`); in `min`, `admin.pprof` is ignored with a warning.
- YAML parsing moved from the archived `gopkg.in/yaml.v3` to the maintained `go.yaml.in/yaml/v3` v3.0.5
  (same API; YAML org fork).
- `.standards` advanced to latest; `release-sbom` / `release-sign` now inherit the canonical
  recipes (arguments passed via `ARGS` from the release workflow).
- Local k8s harness moved from Colima to Apple `container` (`container cluster`). `make
  colima-*` → `make container-{setup,deploy,test,teardown}`; scripts in `scripts/container/`.
  kubectl/helm run in `keel-toolbox`, a container on the bridge network; the host only drives
  the `container` CLI, so nothing depends on host → guest routing or Local Network privacy.
- New `make container-stack-test`: the compose suite against `tests/compose/k8s-stack.yaml`.
  Test ports are hostPorts on all node interfaces (iptables node → pod); the unchanged suite
  runs inside the node. CI keeps `make test-compose` (Docker Compose) and `make test-k8s` (kind).
- Removed `setup-docker-macos` (Colima-based, unused).

### Fixed

- Sidecar upstream transport built for high concurrency: no per-host or idle connection caps
  (reused connections, no TIME_WAIT churn), HTTP/2 to TLS upstreams via ALPN, optional plaintext
  HTTP/2 (`sidecar.upstream_h2c`), no environment proxy, and the TLS branch now has dial, handshake
  and idle timeouts. A hung upstream returns 502 after 90% of `timeouts.write` (response-header deadline),
  leaving the rest of the write deadline to deliver the 502.
- `sidecar.upstream_url` accepts `unix:///path/app.sock` (no TCP port ceiling). Not drop-in: the app
  must listen on the socket and share a volume with Keel.
- HTTPS now negotiates HTTP/2 via ALPN (`h2`, then `http/1.1`); `no_h2` builds offer `http/1.1`
  only. Previously HTTP/2 was never offered, so the `no_h2` tag had no effect.
- ACME cache writes are atomic (0600 temp file, fsync, rename; key before cert), so a crash
  cannot leave a truncated or mismatched pair.
- A failed ACME start is logged (`acme_failed`) instead of discarded. HTTP log-sink batches that
  fail or get a non-2xx answer count toward the dropped-lines metric. Reload before `Run` no
  longer panics with a remote sink configured.
- Shutdown releases the signal handler and the StatsD socket; the sidecar route is registered
  before any listener serves.
- Test stacks pinned by digest and bumped: Prometheus v3.15.1, OpenTelemetry Collector 0.163.2,
  Jaeger 2.22.0, http-echo 1.0.0. BATS helpers no longer use host Python (perl/nc/openssl).
- Colima k8s harness runs end to end again: zero-arg `validate_args` no longer rejects an
  empty argument list (`scripts/colima/*.sh`, `scripts/release/ci.sh`); `deploy.sh` builds
  with `docker --context colima-<profile>` and the relocated Dockerfile; k3s pinned to
  `v1.35.0+k3s1`; the repo is shared into the VM when outside `${HOME}`; the gRPC port
  forwarder carries UDP so H3 is reachable from the host.
- ACME http-01 BATS tests run without root (challenge port 5080) using `keel-max` and a dead
  local CA URL.

### Security

- FIPS builds pin the CMVP-certified Go Cryptographic Module (`GOFIPS140=v1.0.0`, cert #5247) instead of
  `latest`; the release check verifies the module from build info. `/health/fips` and `keel_fips_active` now
  report the runtime FIPS state (as `/version` does) instead of the build tag. `docs/FIPS.md` and related docs
  rewritten for the native module; BoringCrypto references removed.
- ACME port-80 redirect only ever targets a configured `tls.acme.domains` entry; a forged `Host`
  goes to the primary domain (no open redirect). `acme.Manager.HTTPHandler` now takes the domains.
- Inbound mTLS: `tls.client_ca_file` (`KEEL_TLS_CLIENT_CA`, Helm `keel.tls.clientCASecretName`)
  makes HTTPS and H3 require a client certificate from that CA. Docs no longer claim client
  certificates map to principals; identity remains JWT-only.
- Sidecar outbound signing fails closed: a signing error returns 502 instead of forwarding the
  request unsigned, and the client's own `Authorization` is never forwarded when Keel signs.
- Access log `ip` is the socket peer; a client `X-Forwarded-For` is logged verbatim as `xff`.
  Text-mode logs escape control characters (no forged lines from `%0a`). Inbound
  `X-Request-ID` must be 1–128 chars of `[A-Za-z0-9._:-]`, otherwise a fresh ID is issued.
- Middleware order: OWASP headers/limits → shedding → AuthnJWT → ExtAuthz → handler. 401/403/503
  carry the OWASP headers, and the external authorizer only sees authenticated requests.
- Admin port authorization is per endpoint. Probes and `/version` stay open; `/metrics` is
  open unless `admin.metrics_token` is set (Prometheus `authorization` bearer);
  `/admin/reload` and `/debug/pprof/` require `admin.token` (`KEEL_ADMIN_TOKEN`).
  **`/admin/reload` answers 403 until `admin.token` is configured; pprof is off unless
  `admin.pprof: true`.** Tokens are redacted from the SIGUSR1 dump.
- FIPS status (`fips.monitor`, `/version` `fips_active`) comes from `crypto/fips140.Enabled()`;
  `GODEBUG`/`GOFIPS140` env strings can no longer fake FIPS mode.
- New memory guard `backpressure.min_headroom_bytes` (default 10 MiB): answers 503 with
  `Retry-After: 1` while headroom under the effective memory limit is below the floor.
- `authn.trusted_ids` and `backpressure.*` take effect on reload (SIGHUP or `/admin/reload`);
  removing an ID revokes it immediately. Reload docs corrected (`sidecar.upstream_url` is
  restart-required).
- Inbound JWTs must carry `exp`; algorithms are pinned to HS/RS/ES 256/384/512. New optional
  `authn.audience` (enforced `aud`) and `sidecar.upstream_audience` (`aud` on outbound signed
  tokens) stop cross-service token replay. **Tokens without `exp` are now rejected.**
- JWKS: https only (loopback excepted), 200 only, 1 MiB cap, failures cached 30s (last good
  keys served meanwhile), concurrent fetches coalesced.
- ext_authz: config rejects `enabled` with an empty endpoint or non-positive timeout; the
  client no longer follows redirects; OPA responses capped at 1 MiB.
- SIGUSR1 config dump redacts HMAC signer secrets and URL credentials/query strings.
- Circuit breaker no longer counts client-cancelled requests (any client could open it).
- Helm: `networkPolicy.enabled` now defaults to `true` (blocks direct access to a sidecar app
  port); `rbac.create` defaults to `false` (keel does not use the Kubernetes API);
  `serviceAccount.automountToken: false`. **Admin-port scraping from another namespace now
  needs `networkPolicy.adminNamespaceSelector`.**

- Container images trust an explicit allowlist of 10 CA roots (ISRG X1/X2, DigiCert Global G2/G3,
  D-TRUST 2020/2023, Telekom Security 2020/2023) copied from the new `keel-certs` image
  (`Dockerfile.certs`, full Mozilla bundle, `make certs-image`) — previously no CA bundle at all,
  so outbound TLS failed. Every image build path builds `keel-certs` first.
- Unparseable env overrides (e.g. `KEEL_AUTHN=flase`) are logged as `env_ignored` instead of being
  silently dropped.
- Request body/response caps and the read timeout apply even with `security.owasp_headers: false`
  or the `no_owasp` build (new `Limits` middleware).

### Fixed

- Sidecar upstream health is its own readiness flag, reported as the `upstream` check: `/readyz`
  aggregates it (a sidecar is not ready while its upstream is not), and an upstream outage no longer
  clears the backpressure flag — which made the shedding middleware 503 all traffic.
- `/readyz` reports NOT ready during the pre-stop sleep (a check the sidecar health probe
  cannot override).
- Reloads (SIGHUP / `/admin/reload`) wait for startup to finish and are serialized; listener
  goroutines no longer read live config.
- The sidecar's upstream health probe uses the upstream TLS settings (mTLS upstreams can
  become Ready).
- Helm: removed the `preStop` exec hook, which could never run in the `scratch` image; keel's
  built-in `prestop_sleep` covers it.

## [0.9.12] - 2026-08-07

### Changed

- Unit test reporting output.

## [0.9.11] - 2026-08-07

### Changed

- Unit test reporting output.

## [0.9.10] - 2026-08-07

### Added

- `/readyz` stays not-ready until every listener (keel-owned and embedder-installed) has bound.
- Coverage tooling ingested; unit-test seams for reliability paths.

### Changed

- All `main` entry points reduced to one-line calls into the library.
- Non-secret config values no longer redacted in diagnostics.

## [0.9.9] - 2026-08-05

### Fixed

- Docker launch.
- Canonical `.standards` scripts and their verification updated.

## [0.9.8] - 2026-08-05

### Added

- Per-file coverage no-regression gate (baseline + CI job).
- Collector registration seam so embedders publish series on `/metrics`.
- Streaming passthrough: `Flush` propagates through middleware writers.
- Markdown lint, lint-suppression ban, DCO enforcement, CHANGELOG and security issue labels (OpenSSF).
- Helm template API-compliance tests that run without a cluster.

### Changed

- Makefile migrated to the canonical `.standards` include; all workflow steps route through `make`.
- Helm defaults to sidecar mode; docs corrected for OPA, size and YAML claims.
- SBOM generation no longer uses Python; syft pinned and verified.

### Fixed

- Stale binaries cached during coverage runs; SA5011 nil-deref guards.

## [0.9.7] - 2026-03-13

### Added

- OTLP tracing via a zero-dependency custom OTLP/HTTP span exporter (`tracing.otlp.*`
  config block). Enables distributed tracing without pulling in the full OpenTelemetry SDK.

### Security

- No CVEs fixed in this release.

## [0.9.6] - 2026-03-12

### Added

- Full ACME certificate lifecycle: automatic provisioning, cache persistence across
  restarts, and renewal with a 30-day window (`tls.acme.*` config block).
  Pebble-based end-to-end CI test included.
- FIPS 140 monitor: startup gate rejects non-FIPS builds when `fips.monitor = true`;
  background loop re-checks at runtime; `/version` endpoint exposes FIPS status.
- Kind-based Kubernetes CI job with cluster-agnostic `scripts/k8s/` layer.
- macOS runner coverage in the CI unit test matrix.

### Changed

- All previously unwired config fields now hot-reloaded on SIGHUP:
  `logging.*`, `metrics.*`, `timeouts.*`, `authn.*`.
- Build tags and ACME mock configuration updated for test isolation.
- Module dependencies updated.

### Security

- No CVEs fixed in this release.

## [0.9.5] - 2026-03-10

### Added

- Automated semver tag management (`scripts/semver/`): conventional-commit-aware
  version bumping and tag creation.
- YAML schema validation for `keel.yaml` configuration files.

### Security

- No CVEs fixed in this release.

## [0.9.4] - 2026-03-10

### Changed

- Semver tooling patches and stability fixes.

### Security

- No CVEs fixed in this release.

## [0.9.3] - 2026-03-10

### Changed

- Semver tooling patches and stability fixes.

### Security

- No CVEs fixed in this release.

## [0.9.2] - 2026-03-10

### Added

- Initial semver tag management tooling and YAML schema framework.

### Security

- No CVEs fixed in this release.

## [0.9.1] - 2026-03-09

### Added

- Optional upstream authorization (AuthZ) middleware and global config consistency
  checker.
- OCI container image publishing to GitHub Container Registry (ghcr.io).
- Helm chart publishing to GitHub OCI registry.
- Docker Compose integration for local multi-service development.
- SBOM attestation for OCI container images (SPDX format via Syft).
- Code coverage reporting in CI with per-package coverage gates.

### Security

- No CVEs fixed in this release.

## [0.9.0] - 2026-01-25

### Added

- Initial public release of Keel: a minimal, FIPS-capable Go web server library
  and binary with hierarchical YAML configuration, TLS, observability hooks,
  and a 7 MB minimum binary footprint.

### Security

- No CVEs fixed in this release.

[Unreleased]: https://github.com/keelcore/keel/compare/v0.9.12...HEAD
[0.9.12]: https://github.com/keelcore/keel/compare/v0.9.11...v0.9.12
[0.9.11]: https://github.com/keelcore/keel/compare/v0.9.10...v0.9.11
[0.9.10]: https://github.com/keelcore/keel/compare/v0.9.9...v0.9.10
[0.9.9]: https://github.com/keelcore/keel/compare/v0.9.8...v0.9.9
[0.9.8]: https://github.com/keelcore/keel/compare/v0.9.7...v0.9.8
[0.9.7]: https://github.com/keelcore/keel/compare/v0.9.6...v0.9.7
[0.9.6]: https://github.com/keelcore/keel/compare/v0.9.5...v0.9.6
[0.9.5]: https://github.com/keelcore/keel/compare/v0.9.4...v0.9.5
[0.9.4]: https://github.com/keelcore/keel/compare/v0.9.3...v0.9.4
[0.9.3]: https://github.com/keelcore/keel/compare/v0.9.2...v0.9.3
[0.9.2]: https://github.com/keelcore/keel/compare/v0.9.1...v0.9.2
[0.9.1]: https://github.com/keelcore/keel/compare/v0.9.0...v0.9.1
[0.9.0]: https://github.com/keelcore/keel/releases/tag/v0.9.0
