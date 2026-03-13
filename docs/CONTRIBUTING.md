# Contributing to Keel

Thank you for your interest in contributing. This document covers everything you need
to go from idea to merged pull request.

---

## Table of Contents

1. [Before You Start](#1-before-you-start)
2. [Development Setup](#2-development-setup)
3. [Making Changes](#3-making-changes)
4. [Testing](#4-testing)
5. [Submitting a Pull Request](#5-submitting-a-pull-request)
6. [Reporting Issues](#6-reporting-issues)
7. [Security Vulnerabilities](#7-security-vulnerabilities)
8. [Code of Conduct](#8-code-of-conduct)

---

## 1. Before You Start

- Check [open issues](https://github.com/keelcore/keel/issues) and
  [open PRs](https://github.com/keelcore/keel/pulls) to avoid duplicating effort.
- For significant changes (new features, architectural changes, breaking API changes),
  open an issue first to discuss the approach before writing code.
- For small, self-contained fixes (typos, test gaps, documentation errors) you may
  go straight to a PR.

---

## 2. Development Setup

### Prerequisites

- Go (version specified in `go.mod` — use `go-version-file: go.mod` tooling)
- [Helm](https://helm.sh) >= 3.8 (for chart development and consistency tests)
- [BATS](https://github.com/bats-core/bats-core) (for integration and consistency tests)
- [Docker](https://docs.docker.com/get-docker/) (for compose integration tests)

### Clone and initialize

```bash
git clone https://github.com/keelcore/keel.git
cd keel
git submodule update --init --recursive   # initializes .standards/
```

### Verify your setup

```bash
# Unit tests — should pass on any supported platform
make test-unit

# Lint
make lint

# Build all flavors
make build
```

---

## 3. Making Changes

### Coding standards

All changes must follow the [keelcore/standards](https://github.com/keelcore/standards)
governance framework. The standards cover:

- **Coding discipline** — surgical edits, no speculative cleanup, scope control.
  See [`.standards/governance/coding.md`](../.standards/governance/coding.md).
- **CI rules** — scripts-first, supply-chain security, coverage gates.
  See [`.standards/governance/ci.md`](../.standards/governance/ci.md).
- **Bash scripts** — `set -euo pipefail`, `validate_args`, `log` pattern.
  See [`.standards/governance/bash.md`](../.standards/governance/bash.md).

If you use Claude Code, Cursor, or GitHub Copilot, the standards are automatically
loaded as context. See [docs/ai-tooling.md](ai-tooling.md).

### Commit messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
type(scope): short description

Optional longer description explaining the why, not the what.
```

Allowed types: `feat`, `fix`, `chore`, `docs`, `test`, `refactor`, `perf`,
`ci`, `build`, `release`, `hotfix`, `revert`.

Examples:
```
feat(authz): add external authz middleware with OPA and HTTP transports
fix(tls): handle cert reload failure without dropping existing connections
docs(config-reference): add ext_authz section
```

The PR policy gate enforces this format on both PR titles and individual commit
messages. Run `scripts/ci/pr-policy.sh` locally before pushing to check.

### Branch naming

Branches must use one of the allowed prefixes:

```
feat/   fix/   chore/   docs/   test/   refactor/
perf/   ci/    build/   release/  hotfix/  revert/
```

Example: `feat/oidc-proxy`, `fix/tls-reload-race`, `docs/authz-examples`

---

## 4. Testing

### Unit tests

```bash
make test-unit
# or directly:
bash scripts/test/ci.sh
```

Unit tests run with `-race` and coverage. All new code must include tests. The
coverage baseline is enforced in CI — a PR that drops coverage below the baseline
will be blocked.

### Consistency tests (Helm / config sync)

```bash
bats tests/consistency.bats
```

This suite verifies that the Go config struct, Helm chart values, Helm configmap
template, and `docs/config-reference.md` are globally consistent. It runs in CI.

### Compose integration tests

```bash
KEEL_COMPOSE_TESTS=1 make test-compose
# or directly:
KEEL_COMPOSE_TESTS=1 bash scripts/test/compose.sh
```

Requires Docker Compose. Spins up the full stack including keel, upstream, Prometheus,
and the OpenTelemetry collector.

### Integrity tests (BATS)

```bash
bash tests/fixtures/gen-certs.sh   # one-time: generate test TLS certs
bats tests/integrity.bats
```

Validates the compiled binary directly — not a hypothetical output.

### Helm lint

```bash
make lint
# or directly:
bash scripts/lint/helm.sh
```

---

## 5. Submitting a Pull Request

1. **Fork** the repository and create your branch from `main`.
2. **Make your changes** following the coding standards above.
3. **Run the full local test suite** (`make test-unit`, `bats tests/consistency.bats`,
   `make lint`) and confirm everything passes.
4. **Push your branch** and open a PR against `main`.
5. **Fill in the PR description** — the PR policy gate requires at least
   30 characters. Describe what changed and why. Link to the relevant issue if one
   exists (`Closes #N` or `Refs #N`).
6. **Wait for CI** — all required status checks must pass before review.
7. **Respond to review feedback** promptly. Maintainers aim to review within 5
   business days.

### PR checklist

Before requesting review, confirm:

- [ ] `make test-unit` passes
- [ ] `bats tests/consistency.bats` passes (if touching config, Helm, or docs)
- [ ] `make lint` passes
- [ ] New behavior is covered by tests
- [ ] `docs/config-reference.md` updated if config fields were added or changed
- [ ] `docs/security.md` updated if security behavior changed
- [ ] Commit messages follow Conventional Commits

---

## 6. Reporting Issues

Use [GitHub Issues](https://github.com/keelcore/keel/issues). Include:

- Keel version (`keel --version`)
- Deployment mode (library / sidecar)
- Go version and OS/arch
- Minimal reproduction steps or config
- Expected vs actual behavior
- Relevant log output (redact secrets)

---

## 7. Security Vulnerabilities

**Do not open a public issue for security vulnerabilities.**

Follow the process in [SECURITY.md](../SECURITY.md): use GitHub's private
vulnerability reporting or the email contact listed there.

---

## 8. Code of Conduct

This project follows the [CNCF Code of Conduct](CODE_OF_CONDUCT.md).
By contributing, you agree to abide by its terms.
