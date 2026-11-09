# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Breaking changes to any package's public API: major bump. New backward-compatible functionality: minor bump. Fixes:
patch bump. `scripts/release/create-release.local.sh` reads the bump from the entries below.

Security vulnerabilities are reported via [GitHub Security Advisories](https://github.com/keelcore/knobs/security/advisories).

## [Unreleased]

### Changed

- All unit tests for `{foo}.go` are in `{foo}_test.go` beside it (#13). The tests of `branches_test.go`,
  `knobs_test.go`, `servotrim_test.go`, `fuzz_test.go`, `jsonschema_test.go` and `keel_schema_test.go` moved, unchanged,
  into the test file of the source each asserts (new: `bind_test.go`, `config_test.go`, `engine_test.go`,
  `errors_test.go`, `layer_test.go`, `module_test.go`, `rules_test.go`, `secret_test.go`, `strict_test.go`,
  `value_test.go`, `internal/conform/bindings_test.go`); `TestOwnerMustBeAString` moved to `module_test.go`, and
  `TestTypesCommand` to `internal/conform/bindings_test.go`. `spec_test.go`, which runs every conformance case
  against the package, is now `doc_test.go`, the test of the contract `doc.go` describes. The `TestServotrim*` tests
  are renamed after what each tests (`TestLayerPrecedence`, `TestConnectionClampAndReject`, ...), and their fixture
  application is `demo`.
- Schemas and every value are validated by `github.com/santhosh-tekuri/jsonschema/v6` instead of knobs' own subset
  parser (`schema.go`, now gone): every keyword of the schema's dialect is enforced, in draft 2019-09 or 2020-12, and
  a schema may be JSON or YAML. A schema using a keyword outside the old fixed set (`pattern`, `items`,
  `multipleOf`, ...) now loads and is enforced instead of failing with `schema_invalid`; an `x-knobs-` keyword knobs
  does not define, or a schema invalid against its metaschema, still fails. Each module is also validated whole after
  the process layers merge, so `required`, `dependentRequired` and `if`/`then` hold across keys (#12).
- A keyword a schema's draft does not define (a misspelling such as `patern`), at any depth, fails the schema with
  `schema_invalid` instead of being ignored, so no rule written in a schema goes unenforced. The error lists every
  such keyword with the location of the schema object holding it, in `Error.Keywords` (new type `Keyword`) and in the
  conformance harness's error object (`keywords`). Draft 4, 6 and 7 schemas fail with `schema_invalid`: knobs can
  check every keyword of 2019-09 and 2020-12 only (#12).

- `.standards` refreshed from v0.10.24 to v0.11.0: `scripts/dev/run.sh` passes into the dev image each name the script
  it runs imports (keelcore/standards#22); `build-native.sh` records the module root package as `.`
  (keelcore/standards#23); `test-native-integrity.sh` puts the native build on `PATH` in the host's own form and sets
  `NATIVE_DIR` (keelcore/standards#24); the release scripts take the tag from one resolver, `release_tag`
  (keelcore/standards#25); `lint-bash` checks every environment import against `governance/bash.md` R37
  (keelcore/standards#26).

### Fixed

- knobs' own scripts import their environment as `governance/bash.md` R37 requires (`declare -rx NAME="${NAME:-…}"`):
  `apply-rulesets.sh`, `publish-go-module.sh`, `pull-release-module.sh` and the `stub-curl.sh` test fixture, so
  `make lint` passes on `.standards` v0.11.0 and `make check-rulesets` receives `GH_TOKEN` in the dev image (#7).
- `publish-go-module.sh` and `pull-release-module.sh` take the tag from the canonical `release_tag`
  (`scripts/lib/release-tag.sh`): `INPUT_TAG`, else `GITHUB_REF_NAME` only on a tag push. A branch run's
  `GITHUB_REF_NAME` is never taken as the tag (#6, #7).
- `release.yml` no longer assigns `GITHUB_REF_NAME`, which GitHub ignores, nor `KEELCORE_DEV_ENV`: a manual release
  names its tag in `INPUT_TAG` alone, and `run.sh` passes it because the scripts import it. `ci.yml` no longer
  assigns `GITHUB_HEAD_REF`, which GitHub sets itself (#6, #10).
- `build-native.local.sh` leaves the canonical `tests.tsv` as written; its rewrite of the root package's row to `.`
  is gone, as `build-native.sh` now writes `.` itself (#4).
- `test-native-integrity.local.sh` runs `bats` and `knobs-conform` from `PATH` and requires `NATIVE_DIR`, as the
  canonical script now provides both; it no longer searches `target/native/` (#5, #8).
- The release's attest job downloads the module zip with the job's read-only token (`GITHUB_TOKEN`, passed to curl on
  stdin): GitHub answered anonymous release downloads with HTTP 504 while serving authenticated ones, so v0.8.1's
  attestation failed.
- Unit (linux) checks the tree before the Codecov uploader downloads itself into it; the native suites run `bats` and the
  harness from `target/native/<os>-<arch>/` by path, not from `PATH`, whose Windows entries (`D:/...`) split at the
  drive colon.
- A native build records the module root package's directory as `.` (`build-native.local.sh`): the canonical
  `build-native.sh` keeps the dev image's absolute path for it.
- Every release job reads the tag from `inputs.tag` on a manual run (`GITHUB_REF_NAME` there is the branch), so
  the canonical `wait-for-publish.sh` waits for the tag.
- A manual release run takes its tag from `INPUT_TAG`; its `GITHUB_REF_NAME` is the branch.
- CI passes `PR_AUTHOR` to the PR policy check (its Dependabot exemption) and `HEAD_SHA` to the coverage delta gate.
- Dependabot leaves the canonical workflows (`scorecard.yml`, `codeql.yml`) alone: their pins come with the
  `.standards` release. It covers the Go module and its vendor tree.
- Dependabot writes nothing to `main`: version updates open their pull requests against `deps-inbox`, security
  update pull requests are off and the alerts stay on; `make update-pins` takes both as candidates
  (`docs/governance.md`, Dependency updates).
- Dependabot waits 14 days after a release before proposing it, the cooldown `update-pins` applies.

### Added

- `make lint-test-layout` (`scripts/lint/lint-test-layout.sh`), part of `make lint` and, through
  `scripts/lint-all.local.sh` (`make lint-all-local`), the pre-commit hook: a test file with no `{foo}.go` beside
  it, or a source with no `{foo}_test.go`, fails, naming the file; `cmd/*/main.go`, `doc.go` and the helpers-only
  files `config/test-layout.local.txt` lists are exempt (#13).
- Nested objects and arrays in schemas (#12). A property with `properties` of its own is a nested object; any other
  property is a knob of any type, keyed by its dotted path (`demo.listeners.http.port`, env
  `DEMO_LISTENERS_HTTP_PORT`) through every layer. Array text from env, cli, runtime and connection is a JSON list or
  comma-separated items. An application schema (one with no `x-knobs-module`) attaches each of its properties as a
  module, with code values keyed `<module>.<name>` and own files holding the modules directly, so keel's
  `pkg/config/schema.yaml` loads unchanged. New: `TypeArray`, `TypeObject`, `TypeAny`, `Config.List`, and the error
  code `invalid_value` for a value breaking any keyword without its own code (`pattern`, `multipleOf`, ...).
- CI-only checks in a new repository built from the commit (`scripts/lib/scratch-repo.sh`), on a `workflow_dispatch`
  of `ci.yml`: Manual release tag (`make test-manual-release`: `wait-for-publish` and `pull-release-module` take
  v0.8.2 from `INPUT_TAG` on a branch run, and fail without it), and the native build and tests
  (`make build-native-scratch` on Linux, `make test-native-scratch` on macOS and Windows: `tests.tsv` holds no
  absolute path, the root package's tests run, and the integrity hook finds the harness on `PATH`).
- BATS suites `apply-rulesets.bats`, `env-imports.bats`, `workflows-env.bats` (no workflow assigns a `GITHUB_*` or
  `RUNNER_*` variable, nothing names `KEELCORE_DEV_ENV`), `native-hooks.bats`, `native-scratch.bats` and
  `test-manual-release.bats`.
- On Windows, the secrets file is checked by its security descriptor (`perm_windows.go`, `golang.org/x/sys/windows`):
  owned by the current user, SYSTEM or Administrators, with a DACL granting those three only; Linux and macOS keep
  the mode-bit check (`perm_posix.go`). Windows-only tests set DACLs with `github.com/hectane/go-acl`.
- Windows default file locations: `%ProgramData%\<app>\config.yaml` (global) and `%APPDATA%\<app>\config.yaml`
  (user) (`paths_windows.go`); Linux and macOS keep `/etc/<app>` and `$XDG_CONFIG_HOME` or `~/.config/<app>`
  (`paths_posix.go`).
- macOS and Windows CI, as keel runs it: the dev image cross-builds the Go test binaries, bats and
  `knobs-conform` (`make build-native`, with knobs' `build-native.local.sh`); the native runners run the unit tests
  (`make test-native`), the conformance suite on macOS and a harness smoke test on Windows
  (`make test-native-integrity`, `test-native-integrity.local.sh`). `tests/integrity.bats` takes the harness from
  `KNOBS_CONFORM`.
- `x-knobs-owner`: a module schema names the code module that defines it; the master schema adds
  `x-knobs-owner-version` from the binary's build information, and errors on the module's keys name `owner@version`.
- `knobstest`: `Schema` fails a test when the committed master schema differs from the one the code builds
  (`KNOBS_WRITE_SCHEMA=1` rewrites it); `SingleMajor` fails when the binary links two major versions of knobs.
  `knobs.Majors` is its pure check.
- CI uploads unit-test coverage to Codecov on pushes to `main`, with the organization's `CODECOV_TOKEN`.
- `docs/guide.md`, `docs/architecture.md`, `docs/conformance.md`, `docs/testing.md`, `docs/dependencies.md`,
  `docs/ROADMAP.md`.
- `release.yml` attests the module zip's SLSA build provenance after the GitHub Release (`make pull-release-module`
  fetches the zip and checks it against `SHA256SUMS`); `gh attestation verify knobs-vX.Y.Z.zip -R keelcore/knobs`.
- The release workflow (`release.yml`, `github-release.yml`): after CI passes on the tagged commit and a reviewer
  approves the `release` environment, the Go module is published to the checksum database and proxy
  (`make publish-go-module`, knobs's own `scripts/release/publish-go-module.sh`), then the GitHub Release carries
  the module zip, its SBOM, checksums, signatures and provenance. The canonical `.standards` package publishers are
  excluded (`.canonical-exclude`).
- `Module` and `Engine.Attach(modules...)`: each Go module gives the engine its own schema, its code values and its
  own files, so `main` only attaches modules. The lowest layer is `code`: every module's code values, then a
  `Code` layer's (harness `--code`); a schema `default` overrides it as the `schema` layer, which replaces the
  `default` layer. A key marked secret set in code fails with `secret_in_code`.
- Repository bootstrapped to keelcore standards (`.standards` v0.10.24): canonical `Makefile`, scripts, pre-commit
  hook, dev image (`containers/dev`), CI, CodeQL, DCO, Scorecard, dev-image and update-pins workflows, rulesets as
  code (`.github/rulesets/`, `make check-rulesets`), `README.md`, `SECURITY.md`, governance and contribution docs.
- `spec/`: the contract every implementation follows (`spec/README.md`): names, the schema keywords knobs enforces
  (a schema using any other fails), the eight layers and their secret rules, connection overrides, error codes, the
  conformance harness and 44 conformance cases with their test schemas.
- Go package `github.com/keelcore/knobs`: `New`, `Attach` (one schema per module, with optional module files),
  `Add` and `AddStandard` (default, global, user, secrets, env, cli, runtime), `Resolve`; typed accessors, `Bind`,
  `Dump`, `Values`, the master schema; the `Secret` type, which never prints; `Config.WithConnection`, a stateless
  per-connection overlay that holds only its own overrides. Seeded from servotrim's `pipeline/config` and
  `clients/go/knob` (Apache-2.0), whose tests are ported in `servotrim_test.go`.
- `knobs-schema-bump OLD NEW` and `SchemaBump`: keel's configuration release rule, unchanged: a removed schema field
  is major, an added field minor, no change patch, printed in the release hook's form.
- `knobs-conform`, the Go conformance harness; `make build-conform`; `tests/conformance.bats` runs every case against
  it, `tests/schema-types.bats` and `make check-schema-types` audit the Go bindings' types against the test schemas.
- Unit tests for parallel connections and for the memory 100, 1000 and 10000 live connections hold (bounded per
  connection, independent of the count); fuzz targets for text conversion, file layers and connection values.
- `config/gosec-sites.local.txt`: the two reviewed G304 sites, each reading a config path the operator names.
