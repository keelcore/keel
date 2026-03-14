# Governance Standards

Keel enforces a shared set of engineering standards across the keelcore organization via a **git submodule** at
`.standards/`. This document explains what that means, why we do it that way, and how to work with it day-to-day.

---

## What Is `.standards/`?

`.standards/` is a pinned reference to the [`keelcore/standards`](https://github.com/keelcore/standards) repository. It
is a **git submodule** — not a copy of the files. The `keel` repository records the exact commit SHA of the standards it
was last updated to. This means:

- Standards updates are **deliberate** (`git submodule update --remote`, reviewed, committed).
- Every contributor on every clone gets **exactly the same version** of the standards.
- CI can enforce the standards without fetching an unpinned branch.

The submodule root contains:

```text
.standards/
  governance/        ← The canonical standards documents
    bash.md          ← Bash script portability and structure rules
    ci.md            ← CI workflow and supply-chain rules
    coding.md        ← Code review scope, safety, and correctness rules
    observability.md ← Metrics, logging, tracing, alerting rules
    platform.md      ← DNS, TLS, WAF, networking architecture
    security.md      ← IAM, encryption, key management, data classification
    runtime.md       ← Containers, orchestration, deployment, compliance
    api-management.md← API design, versioning, rate limiting, gateway
  docs/
    adr/             ← Architecture Decision Records (numbered, template included)
    rfc/             ← Requests for Comments (proposals for significant changes)
    break-glass.md   ← Emergency access and break-glass procedure
  adapters/          ← Tool-specific adapter files (CLAUDE.md, Cursor, Copilot)
  go/                ← Go tooling for embedding/materializing standards
  scripts/           ← Maintenance scripts for the standards repo itself
```

---

## Why a Submodule (Not Copy-Paste)?

Engineering teams often copy standards documents into each repo. That pattern degrades over time: the copies
drift, the authoritative version becomes unclear, and updates require touching every repo manually.

The submodule pattern solves this by making the relationship explicit and mechanical:

| Concern | Copy-paste | Submodule |
|---|---|---|
| Canonical source | Unclear (who's "right"?) | Always `keelcore/standards` |
| Updates | Manual, error-prone, often skipped | `git submodule update --remote`, one commit |
| Version pinning | None (you have whatever you pasted) | Exact commit SHA, locked in `.gitmodules` and `keel`'s tree |
| Consistency across repos | No guarantee | All repos on the same SHA see the same text |
| AI tool consumption | Stale copies confuse tools | Single path, always current for each repo's pinned SHA |

The submodule is also consumed by AI coding assistants (Claude Code, Cursor, GitHub Copilot) via the adapter files in
`.standards/adapters/`. This means the AI sees the same governance rules that human contributors follow — standards are
not just documentation, they are active constraints on every code change.

---

## How to Use `.standards/` Day-to-Day

### Initial clone

When you clone `keel` fresh, submodules are not fetched automatically unless you use `--recurse-submodules`:

```bash
git clone --recurse-submodules git@github.com:keelcore/keel.git
```

If you already cloned without it:

```bash
git submodule update --init --recursive
```

### Reading the standards

Navigate directly:

```bash
cat .standards/governance/coding.md
cat .standards/governance/ci.md
cat .standards/governance/bash.md
```

Or open them in your editor. The files are plain Markdown — no tooling required.

### Updating to a newer version of the standards

```bash
git submodule update --remote .standards
git add .standards
git commit -m "chore: update standards to latest"
```

After committing, every subsequent `git submodule update --init --recursive` by other contributors will land on the same
new SHA. Pin updates go through normal PR review — a standards bump is a deliberate engineering decision, not an
automatic background fetch.

### Checking which version of the standards is pinned

```bash
git submodule status .standards
```

Output example:

```text
 a3f8c1d2e4b6... .standards (v1.4.2-3-ga3f8c1d)
```

The leading space means the submodule is checked out at the pinned commit. A `+` prefix means the local checkout is
ahead of the pinned commit (you have run `--remote` but not yet committed the pointer update). A `-` prefix means the
submodule has not been initialized.

---

## Architecture Decision Records (ADRs)

The standards repo documents its own design decisions as numbered ADRs under `.standards/docs/adr/`. Key decisions
relevant to Keel contributors:

| ADR | Summary |
|---|---|
| [ADR-0001](.standards/docs/adr/0001-standards-as-submodule-and-package.md) | Why standards are distributed as a submodule (and optionally as a language package) rather than as inline documentation |
| [ADR-0002](.standards/docs/adr/0002-coverage-baseline-in-repo.md) | Why code coverage baselines are stored in the repository rather than in CI secrets or external dashboards |
| [ADR-0003](.standards/docs/adr/0003-architecture-governance-process.md) | The DACI + RFC + ARB process for architecture changes — who decides what, and how |
| [ADR-0004](.standards/docs/adr/0004-workload-identity-spiffe-spire.md) | SPIFFE/SPIRE as the workload identity standard — why not static certs or API keys |
| [ADR-0005](.standards/docs/adr/0005-observability-opentelemetry.md) | OpenTelemetry as the unified observability framework — traces, metrics, and logs under one SDK |
| [ADR-0006](.standards/docs/adr/0006-opa-centralized-policy-engine.md) | OPA (Open Policy Agent) as the centralized authorization engine — policy-as-code |
| [ADR-0007](.standards/docs/adr/0007-zero-downtime-deployment.md) | Zero-downtime deployment via blue/green and canary patterns |
| [ADR-0008](.standards/docs/adr/0008-rapid-key-rotation.md) | 5-minute platform SLA for key rotation — why and how |
| [ADR-0009](.standards/docs/adr/0009-gitops-change-management.md) | GitOps as the declarative change management mechanism — no snowflake ops |

---

## Proposing Changes to the Standards

Standards changes are proposed via RFCs in `.standards/docs/rfc/`. To propose a change:

1. Open an issue or RFC in the `keelcore/standards` repository.
2. If architectural (touches ADR territory), it goes through the ARB process described in [ADR-0003](.standards/docs/adr/0003-architecture-governance-process.md).
3. Corrections and clarifications are patch bumps and can be merged by maintainers without ARB.
4. After the PR is merged and a new version is tagged in `keelcore/standards`, update the submodule pointer in `keel`
   (and any other repos that consume it) via the update process above.

Do not make standards changes directly in `.standards/` within the `keel` repo — that directory is owned by the
submodule. Local edits to `.standards/` are silently discarded on the next `git submodule update`.

---

## Emergency (Break-Glass) Procedures

If CI or production requires a governance exception, follow the break-glass procedure documented in
[.standards/docs/break-glass.md](.standards/docs/break-glass.md). Break-glass events are logged, time-bounded, and
require post-incident review. They are not a mechanism for bypassing standards permanently.
