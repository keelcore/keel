# Break-Glass Procedure

This document describes emergency procedures for bypassing normal CI/CD gates when a critical production issue requires
an immediate fix.

## When to Use

Break-glass is appropriate only when:

- A critical security vulnerability or data-loss bug is in production
- The normal PR + CI pipeline cannot be completed in time
- The risk of delay exceeds the risk of bypassing safeguards

Break-glass is **not** appropriate for feature releases, non-critical bugs, or convenience.

## Authorization

Break-glass requires explicit approval from **two** of the following:

- Repository owner
- A designated maintainer listed in CODEOWNERS

Document the authorization (Slack thread, email, or GitHub issue) before proceeding.

## Procedure

1. **Create a branch** from the exact production commit SHA (not from main if it has diverged).
2. **Apply the minimal fix** — change only what is necessary to address the incident.
3. **Run tests locally**: `make test-unit` must pass before pushing.
4. **Push directly** to `main` using a commit message that includes `[break-glass]` and references the incident issue.
5. **Notify the team** immediately in the incident channel.
6. **Open a follow-up PR** within 24 hours that adds regression tests for the fixed condition.

## Post-Incident

Within 48 hours of the break-glass event:

- [ ] Incident post-mortem filed
- [ ] Regression test merged
- [ ] CI pipeline verified green on the fix commit
- [ ] CODEOWNERS notified and sign-off recorded

## Contacts

See CODEOWNERS for the current list of authorized maintainers.
