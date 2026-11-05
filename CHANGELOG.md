# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Security vulnerabilities are reported via [GitHub Security Advisories](https://github.com/keelcore/keel/security/advisories).
Each release identifies all CVE fixes per [OpenSSF best practices](https://bestpractices.coreinfrastructure.org/projects/12121).

## [Unreleased]

### Added

- `sidecar.retry.honor_retry_after` (default `false`): a 503 or 429 from the upstream carrying `Retry-After` is
  retried once after the stated delay, for an idempotent request whose body can be sent again, within the upstream
  response deadline; counted in `keel_upstream_retry_after_total`.
- Keel appends its own entry to `Via` on every proxied request.
- Protocol rejections (docs/security.md §8, docs/errors.md): every refused TLS handshake is logged
  (`tls_handshake_rejected`) and counted (`keel_tls_handshake_rejections_total{code}`) with a stable `KEEL-PROTO`
  code; plain HTTP on the HTTPS port, and requests below `security.min_http_version`, get a 301 to the notice page
  `/.well-known/keel/upgrade` with an RFC 9457 problem body.
- Docs: `docs/architecture.md`, `docs/threat-model.md`, `docs/dependencies.md`, and branch protection as code
  (`.github/rulesets/`, `make rulesets-check`, `make rulesets-apply`).
- `security.min_http_version` (`1.1` default, `2`, `3`), `security.alt_svc_max_age`, `tls.notice.base_url`,
  `tls.notice.help_url`, `tls.notice.contact`, `tls.client_cert_notice`.
- `logging.file` with `logging.rotation.{max_size_mb,max_age_days,max_backups,compress}`: a file sink rotated by size, age
  and backup count; `SIGUSR2` rotates at once.
- Audit log: `audit.*` records security events as a SHA-256 hash chain (file, syslog or HTTP); `keel audit verify FILE`
  checks it across rotated files. Opt out with `no_audit`.
- OIDC browser login: `authn.oidc.*`, authorization-code flow with PKCE, sealed session cookies, refresh and logout; the
  identity reaches the upstream in `X-Auth-Request-User` and `X-Auth-Request-Email`. Opt out with `no_oidc` or
  `no_authn` (the `min` flavor builds with `no_authn`).
- HTTPS responses advertise HTTP/3 (`Alt-Svc`) when `listeners.h3` is enabled.
- net/http server errors are logged as `http_server_error` (debug) instead of written to stderr.

### Changed

- `.standards` moves to v0.10.13: its tool image now carries govulncheck.
- `make lint-fmt` and `make lint-md` are removed: use `make format` and `make lint-markdown`.
- CodeQL and Scorecard, govulncheck (`make lint-vuln`), `run_parallel` and concurrent `sign.sh`, `run.sh` and `update-pins`
  move to `.standards`; keel carries the canonical copies. CodeQL now finds its languages from the tracked files.
- `make create-release` decides on the pin status committed in `docs/current_pins.md`, with no network query.
- The `keel-min` size gate moves from 8 MB to 10 MB (10,485,760 bytes, linux/amd64).
- The release runs concurrently: binaries, SBOMs, signatures, uploads, images, attestations and verification, with one
  approval gate ahead of all publishing, and each concurrent job's log printed whole.
- Every outbound connection (sidecar upstream, JWKS, external authorization, ACME CA, OTLP exporter, remote log sink)
  uses one TLS policy, the listeners': TLS 1.3 only, with the same key exchange groups. An endpoint limited to TLS 1.2
  now fails to connect.
- External authorization input: `headers` maps each header name to an array with one element per field line (was
  one comma-joined string). Policies read a single-valued header as `input.headers["name"][0]`.
- Without a trusted hop (`sidecar.xff_trusted_hops: 0`), Keel generates its own `traceparent` and `x-request-id`
  instead of continuing the client's; the upstream receives Keel's values.

### Fixed

- The release's image provenance jobs log in to GHCR before pushing their attestations.
- A fixable HIGH or CRITICAL Grype finding, or a failed image build or push, now stops the release: `docker.sh` carried
  on past them, because errexit is off inside a command substitution.
- SIGHUP and `POST /admin/reload` on the `keel` binary re-read the `KEEL_CONFIG` and `KEEL_SECRETS` files. They previously
  reloaded from defaults and environment only, dropping the file's settings.

### Security

- Hop headers (docs/security.md §5.6): a request with two `Authorization` lines is refused with 400; repeated
  `X-Forwarded-For` lines are read as one chain and every entry is normalized to an IP address or `unknown`, so a
  client entry cannot shift `X-Real-IP`; a `traceparent` with non-hex IDs starts a new trace.

## [0.9.16] - 2026-11-04

### Changed

- `make create-release` now runs the canonical `.standards` release tagger. Keel's schema-field diff moved to
  `scripts/release/create-release.local.sh`, which the tagger calls for the bump level; the computed versions and tag
  subjects are unchanged. New `make create-release DRY_RUN=1` shows the next tag without tagging or pushing.
- Helm chart: the image defaults to `ghcr.io/keelcore/keel` with tag `v<appVersion>` (was `keelcore/keel:0.5.0` on
  Docker Hub); `image.flavor: min | fips` now selects the `-min` / `-fips` image; new `image.digest` pins the image.
- Helm chart: default `resources` (requests 100m / 64Mi, limits 500m / 128Mi), so keel always derives `GOMEMLIMIT`
  from a container memory limit.
- Release binaries and images now ship for `linux/amd64` and `linux/arm64`; every image tag is a multi-arch index.
- Image `org.opencontainers.image.created` comes from the commit time (UTC), so rebuilding a tag reproduces it.
- Repository runs in UTC: `TZ=UTC` for every make recipe and workflow; `make lint` rejects non-UTC timestamps.
- CI runners are pinned by image label (`ubuntu-24.04`, `macos-26`, `windows-2025-vs2026`), never `*-latest`; the unit
  matrix jobs are named by platform (`Unit (linux)`, `Unit (macos)`, `Unit (windows)`), so a runner bump never
  renames a required check.
- `make lint-go` runs `go vet` and `staticcheck` after the gofmt check.

### Security

- JWKS EC keys: a key whose point is not on its curve, or whose coordinate is longer than the curve allows, is
  rejected (`ecdsa.ParseUncompressedPublicKey`; the deprecated `X`/`Y` fields are no longer set directly).
- Helm on Linux CI installs the pinned release from get.helm.sh, verified against committed SHA-256 sums (was the
  unversioned apt package); markdownlint installs from a committed lockfile with integrity hashes.
- Release images are scanned before push (Grype; fails on fixable HIGH/CRITICAL), each carries an SBOM generated from
  that image, and each digest gets SLSA build provenance in GHCR. Images are signed by digest before `vX.Y`, `vX` and
  `latest` move; a retried sign or attest that hits an existing Rekor entry is confirmed with `cosign verify` instead
  of failing the release.
- CI gates: `govulncheck` (Vulnerability Scan), `gosec` (SAST), CodeQL (`security-extended`), and a fuzz pass over
  new native fuzz targets for JWKS, JWT, PEM key, ext_authz, ACME host matching and config YAML parsing.
- Base images are pinned by digest; Dependabot now tracks Docker base images and the `.standards` submodule.
- The `pkg/apps/myapp` client listener sets `ReadHeaderTimeout` (gosec G114).
- The release verifies its own output as its last step (`make release-verify`): signatures, SBOM, OpenVEX and SLSA
  provenance on every image and binary, the aliases, and the chart signature.
- OpenVEX: each release generates an OpenVEX document from govulncheck reachability (`make release-vex`), validates
  it (`make lint-vex`), attests it to every image index and attaches it to the GitHub Release (e.g. GO-2026-5932,
  `golang.org/x/crypto/openpgp`, not reachable from keel). It is never committed.
- New CI gates: OpenSSF Scorecard (`scorecard.yml`), workflow security lint (actionlint + zizmor), chart policy scan
  (kubescape, `helm-lint.yml`), dependency license allowlist (go-licenses).
- Workflows: `persist-credentials: false` on every checkout; the release build runs without the Go module cache;
  action pin comments corrected to the tags the pinned commits carry (`setup-go` v5.1.0, `upload-artifact` v4.5.0).
- keel no longer carries or verifies the `.standards` package publishers (`go-publish.sh`, `npm-publish.sh`,
  `pypi-publish.sh`); they are listed in `.canonical-exclude`.

## [0.9.13] - 2026-10-30

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

[Unreleased]: https://github.com/keelcore/keel/compare/v0.9.13...HEAD
[0.9.13]: https://github.com/keelcore/keel/compare/v0.9.12...v0.9.13
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
