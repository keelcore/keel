# Dependency Policy

Keel ships as a small, auditable binary, and every dependency is part of what it ships. This policy covers choosing,
adding, updating and removing dependencies.

## Selection Criteria

**Standard library first.** A dependency is added only when the standard library cannot do the job at a reasonable
cost. A new dependency needs all of the following, stated in the pull request:

- **Maintenance:** active releases and responsive maintainers.
- **Security history:** its CVE record, how quickly those were fixed, and a clean `make lint-vuln`.
- **License:** on the allow list that `make lint-licenses` enforces: `Apache-2.0`, `BSD-2-Clause`, `BSD-3-Clause`,
  `MIT`, `ISC`.
- **Binary size:** the impact measured with `make report-size`. `keel-min` stays under its 10 MB gate.
- **Build tags:** a feature dependency sits behind its opt-out tag (for example `no_h3`), so the minimal build excludes
  it.
- **FIPS:** cryptography comes from the Go Cryptographic Module (the standard library). A dependency that brings its
  own primitives is excluded from the FIPS build, or rejected.

## Procedures

**Add or update:**

1. `go get <module>@<version>`, then `go mod tidy` and `go mod vendor`. The `vendor/` directory is committed, for
   reproducible and air-gapped builds.
2. Run `make lint-vuln lint-licenses report-size test`.
3. Commit `go.mod`, `go.sum` and `vendor/` together.

Routine updates go through `make update-pins` and `make update-pins APPLY=1`, which verify each update before writing
it.

**Remove:** delete the use, run `go mod tidy` and `go mod vendor`, and commit all three together.

## Tracking

| What | Where |
| --- | --- |
| Direct and indirect modules | `go.mod`, `go.sum`, `vendor/` |
| Current pins of every kind | `make update-pins` report |
| Update proposals | Dependabot (`.github/dependabot.yml`) and `update-pins` |
| Known vulnerabilities | `make lint-vuln` (govulncheck) in CI; OpenVEX statements in each release |
| What each release contains | SBOM per artifact and image |
