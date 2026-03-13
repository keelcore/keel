# AI Tooling Integration

Keel ships first-class adapter files for AI coding assistants. These adapters wire the [`keelcore/standards`](https://github.com/keelcore/standards) governance documents directly into the context that AI tools see when they work in this repository — so the same rules that apply to human contributors also apply to AI-generated suggestions.

The adapter files live inside the `.standards/` submodule and are activated in this repository via **symlinks** committed to the tree. The symlinks are already in place — no setup is required after a normal clone. They update automatically when the `.standards` submodule is bumped.

---

## Why This Matters

AI coding assistants are productive but permissive by default. Without explicit instructions they will:

- Ignore project-specific bash conventions (`set -euo pipefail`, logging patterns, `validate_args` structure).
- Generate CI steps with inline shell logic instead of delegating to `scripts/`.
- Add speculative features, unnecessary abstractions, or error handling for cases that cannot occur.
- Use deprecated APIs, skip FIPS guards, or miss build-tag requirements.

The adapter files solve this by giving each tool a concise, authoritative reference to the governance standards before it generates anything. This is not a style guide comment in a PR — it is active context loaded on every interaction.

---

## Active Adapters

All adapters are symlinks into `.standards/adapters/`. They are committed to the repository tree and require no manual activation.

### Claude Code

| Symlink | Target |
|---|---|
| `CLAUDE.md` | `.standards/adapters/claude/CLAUDE.md` |

**Source:** [adapters/claude/CLAUDE.md](https://github.com/keelcore/standards/blob/main/adapters/claude/CLAUDE.md)

Claude Code automatically loads `CLAUDE.md` from the repository root on every invocation. The adapter references the governance documents using relative paths so Claude resolves them from within the submodule. No additional configuration is needed.

### Cursor

| Symlink | Target |
|---|---|
| `.cursor/rules/coding.mdc` | `.standards/adapters/cursor/coding.mdc` |
| `.cursor/rules/ci.mdc` | `.standards/adapters/cursor/ci.mdc` |
| `.cursor/rules/bash.mdc` | `.standards/adapters/cursor/bash.mdc` |

**Sources:** [adapters/cursor/](https://github.com/keelcore/standards/blob/main/adapters/cursor/)

Each `.mdc` file carries `alwaysApply: true` front-matter, which tells Cursor to include the rule in every request regardless of file type. Coding, CI, and bash standards are applied as three separate rules so Cursor can surface the most relevant one in its UI without truncating a single large document.

### GitHub Copilot

| Symlink | Target |
|---|---|
| `.github/copilot-instructions.md` | `.standards/adapters/copilot/copilot-instructions.md` |

**Source:** [adapters/copilot/copilot-instructions.md](https://github.com/keelcore/standards/blob/main/adapters/copilot/copilot-instructions.md)

GitHub Copilot reads `.github/copilot-instructions.md` as repository-level instructions and prepends them to every inline suggestion and chat response for contributors working in this repository.

---

## Keeping Adapters Current

The adapters are part of the `.standards` submodule. Because they are symlinks, they resolve to whatever files the currently pinned submodule SHA contains — no re-copying needed after a submodule update.

To update the pinned standards version (and therefore all adapter content):

```bash
git submodule update --remote .standards
git add .standards
git commit -m "chore: update standards"
```

See [docs/governance.md](governance.md) for the full explanation of the submodule pattern, version pinning, and how to propose changes upstream.

---

## What the Standards Cover

Each adapter references the governance documents most relevant to AI-assisted work. The full governance set is:

| Standard | What it governs |
|---|---|
| [`governance/coding.md`](https://github.com/keelcore/standards/blob/main/governance/coding.md) | Scope control, surgical edits, no speculative cleanup, safety, reviewability |
| [`governance/ci.md`](https://github.com/keelcore/standards/blob/main/governance/ci.md) | CI workflow structure, supply-chain security, coverage, least-privilege permissions |
| [`governance/bash.md`](https://github.com/keelcore/standards/blob/main/governance/bash.md) | `set -euo pipefail`, logging, `validate_args` pattern, portability |
| [`governance/observability.md`](https://github.com/keelcore/standards/blob/main/governance/observability.md) | Metrics naming, log field conventions, trace propagation, alerting rules |
| [`governance/security.md`](https://github.com/keelcore/standards/blob/main/governance/security.md) | IAM, encryption at rest and in transit, key management, data classification |
| [`governance/runtime.md`](https://github.com/keelcore/standards/blob/main/governance/runtime.md) | Container base images, resource limits, orchestration, compliance controls |
| [`governance/api-management.md`](https://github.com/keelcore/standards/blob/main/governance/api-management.md) | API versioning, rate limiting, quota design, gateway patterns |
| [`governance/platform.md`](https://github.com/keelcore/standards/blob/main/governance/platform.md) | DNS, TLS policy, WAF, networking architecture governance process |
