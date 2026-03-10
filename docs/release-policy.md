# Release Policy

This document covers how Keel versions are tagged and released. The authoritative
policy lives in the [keelcore/standards `ci.md`](../.standards/governance/ci.md)
under **Release Tagging**. This document is the developer-facing how-to that
complements that policy.

---

## Versioning Specification

Keel follows **Semantic Versioning 2.0.0** — [https://semver.org](https://semver.org).
That document is the canonical reference; the rules below are derived from it and
must not contradict it.

Key semver.org rules in force here:

- Version numbers take the form `MAJOR.MINOR.PATCH` where each component is a
  non-negative integer with no leading zeroes.
- MAJOR is incremented for incompatible API changes; MINOR and PATCH reset to 0.
- MINOR is incremented for new backward-compatible functionality; PATCH resets to 0.
- PATCH is incremented for backward-compatible bug fixes only.
- Major version zero (`0.y.z`) is for initial development. The public API is not
  yet stable and anything may change at any time.
- `1.0.0` defines the first stable public API.
- Pre-release identifiers (`-alpha.1`, `-rc.2`, etc.) and build metadata (`+sha`)
  are valid semver extensions but are **not supported by `create-release.sh`** at
  this time. `--force` only accepts bare `MAJOR.MINOR.PATCH` versions.

**Scope of automated detection:** The script detects breaking changes by diffing
the config YAML field surface (`pkg/config/schema.yaml`). Other API-breaking
changes — Go type signatures, HTTP API shape, security policy, behavior semantics
— are not automatically detected. Use `--force` with the appropriate bump level
for those cases.

---

## Core Rule: Tags Are Developer-Created, Never CI-Created

CI reacts to pushed tags to run the release pipeline. CI never creates tags.
A version tag is a human commitment about what a commit represents — that
decision requires review and cannot be automated.

---

## Tooling

| Script | Purpose |
|---|---|
| `scripts/release/create-release.sh` | Compute, approve, tag, and push a release |
| `scripts/release/gen-schema.sh` | Regenerate `pkg/config/schema.yaml` after config changes |
| `cmd/config-schema/main.go` | Go tool: reflection-walk of `config.Config` → dotted YAML paths |
| `make gen-schema` | Alias for `gen-schema.sh` |
| `make create-release` | Alias for `create-release.sh` (auto mode) |
| `make create-release FORCE=v1.0.0` | Alias for `create-release.sh --force v1.0.0` |

---

## How Version Bumps Are Computed

The script diffs `pkg/config/schema.yaml` at the previous tag against the file
at `HEAD`. The schema lists every fully-flattened dotted YAML field path in
`config.Config` (e.g. `sidecar.circuit_breaker.reset_timeout`).

| Schema change | Bump level | semver.org rationale |
|---|---|---|
| One or more fields **removed** | **major** — breaking change | Removing a field is an incompatible API change |
| One or more fields **added**, none removed | **minor** — new feature | New fields are backward-compatible additions |
| No field surface changes | **patch** — internal improvements | No public API surface change |

---

## Normal Release Flow

```
git checkout main && git pull --ff-only
# ... merge your work ...
./scripts/release/create-release.sh
```

The script will:

1. Verify you are on `main`, your branch's configured upstream is in sync, and the working tree is clean.
2. Compute the field diff and determine the bump level.
3. Print the summary (fields added/removed, bump level, proposed tag message).
4. Prompt for `y/N` approval.
5. On approval: create a signed annotated tag and push it to the upstream remote.

CI picks up the pushed tag and runs the release pipeline (build, sign, SBOM,
publish).

---

## Forcing a Specific Version

Use `--force vX.Y.Z` to override the computed version. Only bare `MAJOR.MINOR.PATCH`
versions are accepted — pre-release extensions (`-alpha.1`) and build metadata
(`+sha`) are not supported.

```bash
./scripts/release/create-release.sh --force v1.0.0
```

### Pre-1.0 rules (current major = 0)

Per semver.org, `0.y.z` is for initial development where the API is not yet stable.
Only the following targets are accepted:

- Any `0.x.y` where `0.x.y > current` — normal initial-development progression.
- Exactly `v1.0.0` — the stable API promotion, regardless of detected bump level.

Forcing `v1.1.0`, `v2.0.0`, or any other version outside this range is rejected.

### Post-1.0 rules (current major ≥ 1)

Once `1.0.0` is released the public API is stable. `--force` is restricted to
exact single-step increments per semver.org's ordering rules:

| `--force` target | Condition | Result |
|---|---|---|
| `cur_maj.cur_min.(cur_pat+1)` | any | **Allowed** — single patch step |
| `cur_maj.(cur_min+1).0` | any | **Allowed** — single minor step (e.g. internal improvements not reflected in schema diff) |
| `(cur_maj+1).0.0` | breaking changes detected AND matches computed version | **Allowed** |
| `(cur_maj+1).0.0` | no breaking changes, or doesn't match computed | **Rejected** — major bump requires a detected breaking change |
| anything else | — | **Rejected** — must be a single-step increment |

---

## Keeping schema.yaml Fresh

`pkg/config/schema.yaml` is a committed artifact generated from `config.Config`
by reflection. The pre-commit hook regenerates and stages it automatically
whenever any `pkg/config/*.go` file is part of a commit — no manual step required.

To regenerate it outside of a commit (e.g. to inspect the current output):

```bash
./scripts/release/gen-schema.sh
# or
make gen-schema
```

The consistency test suite enforces that `schema.yaml` is never stale — a
mismatch between the committed file and the reflection output fails CI.

---

## What Goes in the Tag Message

The script auto-generates a signed annotated tag with a structured message:

- **Subject line:** `{bump}: {short summary}` (e.g. `minor: added 2 config field(s)`)
- **Body:** full field list, bump level, previous/new version, commit SHA, and
  provenance trailers (`Generated-by:`, `Schema:`)

The GitHub Release body is populated directly from the tag message via
`gh release create --notes-from-tag`.

---

## Emergency / Break-Glass

If a critical patch must ship and the normal flow is blocked, follow the
break-glass procedure in `docs/break-glass.md`. The bypass still requires two
senior engineers and an incident record — it does not bypass the tag-creation
requirement, only the precondition checks.
