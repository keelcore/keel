# Release Policy

Keel releases follow the keelcore standards release policy,
[`.standards/governance/release-policy.md`](../.standards/governance/release-policy.md),
which applies in full: semver rules, developer-only tagging, the release flow,
`--force` rules, pre-1.0 demotion and the tag message format. The authoritative
policy is [`ci.md`](../.standards/governance/ci.md) under **Release Tagging**.

This document covers only keel's customization, as that policy's **Project
Customization** section provides.

**Release checklist (keel):** for each MINOR release, or when keel gains an external interface, review
[threat-model.md](threat-model.md) and update it in the same release.

---

## Keel's Bump Hook

`scripts/release/create-release.local.sh` diffs `pkg/config/schema.yaml` at the
previous tag against the file at `HEAD`. The schema lists every fully-flattened
dotted YAML field path in `config.Config` (e.g.
`sidecar.circuit_breaker.reset_timeout`).

| Schema change | Hook reports | Tag subject |
| --- | --- | --- |
| One or more fields **removed** | `major` — breaking change | `removed N config field(s)` |
| One or more fields **added**, none removed | `minor` — new feature | `added N config field(s)` |
| No field surface changes | `patch` — internal improvements | `internal improvements; no config surface changes` |

**Why the config surface:** operators depend on keel through its YAML
configuration. Removing a field breaks an existing `keel.yaml`; adding one does
not.

---

## What the Hook Cannot See

The hook sees only the config field surface. These breaking changes need a
developer decision and `make create-release FORCE=vX.Y.Z`:

- Go type signatures in the library API (`pkg/core`, `pkg/config`).
- HTTP API shape (admin, probe and health endpoints).
- Security policy (authn, authz, TLS defaults).
- Behavior semantics behind an unchanged field.

---

## Keeping schema.yaml Fresh

`pkg/config/schema.yaml` is a committed artifact generated from `config.Config`
by reflection. The pre-commit hook regenerates and stages it automatically
whenever any `pkg/config/*.go` file is part of a commit — no manual step required.

To regenerate it outside of a commit (e.g. to inspect the current output):

```bash
./scripts/release/generate-schema.sh
# or
make generate-schema
```

The consistency test suite enforces that `schema.yaml` is never stale — a
mismatch between the committed file and the reflection output fails CI.

---

## Keel Tooling

| Script or target | Purpose |
| --- | --- |
| `scripts/release/create-release.local.sh` | Keel's bump hook: schema field diff → bump level and tag subject |
| `scripts/release/generate-schema.sh` | Regenerate `pkg/config/schema.yaml` after config changes |
| `cmd/config-schema/main.go` | Go tool: reflection-walk of `config.Config` → dotted YAML paths |
| `make generate-schema` | Alias for `generate-schema.sh` |
| `make create-release-local PREV_TAG=v0.9.13` | Run the hook alone against a previous tag |
| `scripts/release/update-pins.local.sh` | Keel's pin hook (`make update-pins`): pins, holds, verification targets, appliers |
| `make update-pins-local WHAT=pins` | Run the pin hook alone (`pins`, `holds`) |

---

## Pin Refresh

The canonical `make update-pins` (`governance/ci.md` "Pin Refresh") runs before every release; stale pins block
`make create-release`. Keel's hook adds:

- **Pins:** kubectl and helm in the toolbox `Containerfile` (downloaded from dl.k8s.io and get.helm.sh), resolved
  against their GitHub releases and applied with the checksums those CDNs publish.
- **Holds:** the locally built `keel:test` and `keel-mockupstream:test` images, the `example.invalid` Helm fixture
  image, the `REQUIRED_GO` toolchain-prefix check, and `KUBE_MIN` (the oldest Kubernetes API the chart supports).
- **Verification:** Go modules run `make build-min build-max test`; each Go tool installs and runs its own lint target (gosec,
  govulncheck, actionlint, go-licenses); toolbox tools rebuild a separate `keel-toolbox-pins` toolbox.
- **Combined run:** `build-min build-max lint audit test`, then the integration stack: Apple `container`
  (`setup-container test-container-stack`, in `keel-toolbox-pins`, which mounts the throwaway worktree) on macOS,
  Docker Compose (`test-compose`) on Linux. `keel-toolbox-pins` is removed when the run ends.

---

## Tag Message Additions

Keel's hook adds its findings to the tag body:

```text
Schema: pkg/config/schema.yaml (since v0.9.13)
Fields removed (BREAKING): none
Fields added:
  sidecar.upstream_h2c
```
