# Security Policy

This document describes how to report a vulnerability in knobs, how reports are handled, and which versions receive
fixes.

Related: [threat model](docs/threat-model.md).

---

## Security Contacts

| Name            | GitHub                                           | Email                                         |
| --------------- | ------------------------------------------------ | --------------------------------------------- |
| JP Charlton Jr. | [@PaulCharlton](https://github.com/PaulCharlton) | [security@byiq.com](mailto:security@byiq.com) |

---

## Reporting a Vulnerability

Report vulnerabilities privately; a public issue reveals the vulnerability before it is fixed.

### Option 1: GitHub Private Vulnerability Reporting (preferred)

[Report a vulnerability](https://github.com/keelcore/knobs/security/advisories/new) from the repository's
**Security** tab. The report is visible only to the maintainers until it is disclosed.

### Option 2: Email

Send the report to **[security@byiq.com](mailto:security@byiq.com)**. For sensitive details, ask for a PGP key in the
first message.

### What to Include

- **Description:** what the vulnerability allows an attacker to do.
- **Affected implementation and layer:** the language package, and the layer (file, env, cli, connection, secrets,
  schema validation).
- **Reproduction:** schema, inputs and commands that show it.
- **Impact:** your severity estimate.
- **Versions affected.**

---

## Triage and Response Timeline

| Milestone               | Target                                                            |
| ----------------------- | ----------------------------------------------------------------- |
| Initial acknowledgement | Within 2 business days of receipt                                 |
| Severity assessment     | Within 5 business days                                            |
| Fix development begins  | Within 10 business days for Critical/High; 30 days for Medium/Low |
| Patch release           | Within 30 days for Critical/High; 90 days for Medium/Low          |
| Public disclosure       | After the patch release, coordinated with the reporter            |

Severity follows [CVSS v3.1](https://www.first.org/cvss/v3.1/specification-document). A confirmed vulnerability of
Medium severity or higher gets a CVE through a GitHub Security Advisory, and the fix is marked in `CHANGELOG.md` under
`### Security` with its CVE.

---

## Supported Versions

| Version                | Status                                              |
| ---------------------- | --------------------------------------------------- |
| Latest minor release   | Receives security fixes                             |
| Previous minor release | Receives security fixes for 6 months after the next |

All five packages share one version, so a fix ships in every language's package of the patch release.

---

## Coordinated Disclosure

1. The reporter submits a private report.
2. The maintainers acknowledge, assess and fix it.
3. Maintainers and reporter agree on a disclosure date: the patch release, or 90 days after the report, whichever
   comes first.
4. The patch is released and the advisory published, crediting the reporter unless they prefer otherwise.

The maintainers act in good faith and take no legal action against reporters who follow this policy.

---

## Supply Chain

Every release artifact (the Go module's source archive, the crate, the npm and PyPI packages, the C/C++ archives)
carries its SPDX SBOM, a checksum in `SHA256SUMS`, a cosign signature and SLSA provenance, attached to the GitHub
release. Dependency vulnerabilities are scanned in CI for every language (`govulncheck`, `cargo audit`,
`osv-scanner`).

---

## Security Design Principles

1. **Fail closed:** an invalid layer stops loading; a connection override on an unauthorized connection is refused.
2. **Secrets stay secret:** a secret value prints as `<redacted>` in every dump, audit record and error, and the
   secrets file loads only when its owner alone can read it.
3. **Strict schemas:** every layer is validated against the application's schema; unknown keys are errors.
