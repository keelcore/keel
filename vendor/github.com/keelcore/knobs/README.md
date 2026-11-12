# knobs

[![CI](https://img.shields.io/github/actions/workflow/status/keelcore/knobs/ci.yml?branch=main)](https://github.com/keelcore/knobs/actions)
[![CodeQL](https://img.shields.io/github/actions/workflow/status/keelcore/knobs/codeql.yml?branch=main&label=CodeQL)](https://github.com/keelcore/knobs/actions/workflows/codeql.yml)
[![Release](https://img.shields.io/github/v/release/keelcore/knobs)](https://github.com/keelcore/knobs/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/keelcore/knobs.svg)](https://pkg.go.dev/github.com/keelcore/knobs)
[![Coverage](https://img.shields.io/codecov/c/github/keelcore/knobs)](https://codecov.io/gh/keelcore/knobs)
[![Security](https://img.shields.io/badge/security-SECURITY.md-blue)](./SECURITY.md)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/keelcore/knobs/badge)](https://scorecard.dev/viewer/?uri=github.com/keelcore/knobs)
[![SBOM](https://img.shields.io/badge/SBOM-available-brightgreen)](#supply-chain)
[![SLSA 2](https://slsa.dev/images/gh-badge-level2.svg)](https://github.com/keelcore/knobs/attestations)
[![License](https://img.shields.io/github/license/keelcore/knobs)](./LICENSE)

A layered configuration engine: one JSON Schema contract, one precedence order, one set of conformance cases,
implemented natively in each language that uses it: Go first, then Rust, TypeScript, Python and C/C++.

## Layers

Lowest to highest precedence; a key takes the value of the highest layer that sets it.

| # | Layer | Source |
| --- | --- | --- |
| 1 | code | values the application sets in code |
| 2 | schema | the schema's `default` for the key, when it gives one |
| 3 | global file | `/etc/<app>/config.yaml` (the file name is the knob `<app>.knobs.file_name`) |
| 4 | user file | `$XDG_CONFIG_HOME/<app>/config.yaml` (the same file name) |
| 5 | secrets file | `<app>-secrets.yaml`, readable by its owner only; every value from it is secret |
| 6 | env | `<APP>_<MODULE>_<NAME>` |
| 7 | cli | `--knob <app>.<module>.<name>=<value>` |
| 8 | runtime | values the application sets while running |
| 9 | connection | values the caller sends with a connection, on allowlisted keys, when the connection is authorized |

Every layer is validated against the application's schema before it is merged: unknown keys, wrong types and values
out of bounds fail loading, naming the source and the key. Secrets print as `<redacted>` in every dump, audit record
and error, and the secret type redacts itself when printed. A secret comes only from the secrets file, env or code,
never from a plain file or the command line.

Connection values are the caller's per-connection state: the application maps whatever the caller sends
(connection headers, call metadata) to keys, and knobs applies them over the process configuration for that
connection only, keeping no state between connections. A connection costs memory in proportion to its own
overrides.

## Packages

| Language | Package | Install |
| --- | --- | --- |
| Go | `github.com/keelcore/knobs` | `go get github.com/keelcore/knobs@latest` |

The Rust, TypeScript, Python and C/C++ packages follow, built against the same `spec/`; every package shares one
version, the release tag.

## Go

Each Go module owns its knobs: its schema, its code values and its own files.

```go
// package httpd

//go:embed knobs.json
var schema []byte

// Knobs is what main attaches: the schema names the module (x-knobs-module: http).
var Knobs = knobs.Module{
    Schema: schema,
    Code:   map[string]any{"workers": runtime.NumCPU()},
}
```

`main` creates the one engine and attaches each module:

```go
engine, err := knobs.New("demo")
if err != nil {
    return err
}
if err := engine.Attach(httpd.Knobs, store.Knobs); err != nil { // each module brings its own knobs
    return err
}
if err := engine.AddStandard(knobs.Standard{Args: os.Args[1:]}); err != nil { // --knob arguments; env read by knobs
    return err
}
config, err := engine.Resolve() // every layer validated; errors name the layer and key
if err != nil {
    return err
}
port, _ := config.Int("http", "port")

// Per connection: the application maps what the caller sent to keys.
conn, attempts := config.WithConnection(knobs.Connection{
    Values:     map[string]string{"demo.http.port": callerPort},
    Authorized: callerAuthorized,
})
```

`knobs-schema-bump OLD NEW` reports the semver bump a schema change calls for (a removed field: major; an added
field: minor; otherwise patch), in the form a release hook prints.

## Conformance

`spec/` holds the test schemas and the conformance cases; `tests/conformance.bats` runs every case against every
implementation's harness, so every package resolves the same inputs to the same result.

## Supply chain

Every release artifact carries its SPDX SBOM, a SHA-256 checksum, a cosign signature and SLSA provenance. See
[SECURITY.md](SECURITY.md).

## Governance

Engineering standards come from [`keelcore/standards`](https://github.com/keelcore/standards), pinned as the
`.standards` submodule; see [docs/governance.md](docs/governance.md). Contributions follow
[docs/CONTRIBUTING.md](docs/CONTRIBUTING.md); every commit is signed off (DCO).

## License

[Apache-2.0](LICENSE).
