# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Security vulnerabilities are reported via [GitHub Security Advisories](https://github.com/keelcore/keel/security/advisories).
Each release identifies all CVE fixes per [OpenSSF best practices](https://bestpractices.coreinfrastructure.org/projects/12121).

## [Unreleased]

### Changed

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

- Colima k8s harness runs end to end again: zero-arg `validate_args` no longer rejects an
  empty argument list (`scripts/colima/*.sh`, `scripts/release/ci.sh`); `deploy.sh` builds
  with `docker --context colima-<profile>` and the relocated Dockerfile; k3s pinned to
  `v1.35.0+k3s1`; the repo is shared into the VM when outside `${HOME}`; the gRPC port
  forwarder carries UDP so H3 is reachable from the host.
- ACME http-01 BATS tests run without root (challenge port 5080) using `keel-max` and a dead
  local CA URL.

### Security

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

[Unreleased]: https://github.com/keelcore/keel/compare/v0.9.7...HEAD
[0.9.7]: https://github.com/keelcore/keel/compare/v0.9.6...v0.9.7
[0.9.6]: https://github.com/keelcore/keel/compare/v0.9.5...v0.9.6
[0.9.5]: https://github.com/keelcore/keel/compare/v0.9.4...v0.9.5
[0.9.4]: https://github.com/keelcore/keel/compare/v0.9.3...v0.9.4
[0.9.3]: https://github.com/keelcore/keel/compare/v0.9.2...v0.9.3
[0.9.2]: https://github.com/keelcore/keel/compare/v0.9.1...v0.9.2
[0.9.1]: https://github.com/keelcore/keel/compare/v0.9.0...v0.9.1
[0.9.0]: https://github.com/keelcore/keel/releases/tag/v0.9.0
