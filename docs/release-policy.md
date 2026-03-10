# Release Policy

This document covers how Keel versions are tagged and released. The authoritative
policy lives in the [keelcore/standards `ci.md`](../.standards/governance/ci.md)
under **Release Tagging**. This document is the developer-facing how-to that
complements that policy.

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

| Schema change | Bump level |
|---|---|
| One or more fields **removed** | **major** — breaking change |
| One or more fields **added**, none removed | **minor** — new feature |
| No field surface changes | **patch** — internal improvements |

---

## Normal Release Flow

```
git checkout main && git pull --ff-only
# ... merge your work ...
./scripts/release/create-release.sh
```

The script will:

1. Verify you are on `main` at `HEAD` of `origin/main` with a clean tree.
2. Compute the field diff and determine the bump level.
3. Print the summary (fields added/removed, bump level, proposed tag message).
4. Prompt for `y/N` approval.
5. On approval: create the annotated tag and push it to `origin`.

CI picks up the pushed tag and runs the release pipeline (build, sign, SBOM,
publish).

---

## Forcing a Specific Version

Use `--force vX.Y.Z` to override the computed version:

```bash
./scripts/release/create-release.sh --force v1.0.0
```

### Pre-1.0 rules (current major = 0)

Only the following targets are accepted:

- Any `0.x.y` where `0.x.y > current` — normal pre-release progression.
- Exactly `v1.0.0` — the 1.0 promotion, regardless of detected bump level.

Forcing `v1.1.0`, `v2.0.0`, or any other version outside this range is rejected.

### Post-1.0 rules (current major ≥ 1)

`--force` is restricted to exact single-step increments:

| `--force` target | Condition | Result |
|---|---|---|
| `cur_maj.cur_min.(cur_pat+1)` | any | **Allowed** — single patch step |
| `cur_maj.(cur_min+1).0` | any | **Allowed** — single minor step (e.g. internal improvements) |
| `(cur_maj+1).0.0` | breaking changes detected AND matches computed version | **Allowed** |
| `(cur_maj+1).0.0` | no breaking changes, or doesn't match computed | **Rejected** |
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

The script auto-generates the annotated tag message from the diff:

- **major:** `major: removed N config field(s): field.a field.b …`
- **minor:** `minor: added N config field(s): field.a field.b …`
- **patch:** `patch: internal improvements; no config surface changes`

---

## Emergency / Break-Glass

If a critical patch must ship and the normal flow is blocked, follow the
break-glass procedure in `docs/break-glass.md`. The bypass still requires two
senior engineers and an incident record — it does not bypass the tag-creation
requirement, only the precondition checks.
