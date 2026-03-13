# Security Policy

This document describes the security policy for the Keel project, including how to report vulnerabilities, our triage and disclosure process, supported versions, and how we handle CVEs.

---

## Security Contacts

| Name | GitHub | Email |
|---|---|---|
| JP Charlton Jr. | [@PaulCharlton](https://github.com/PaulCharlton) | security@byiq.com |

For all vulnerability reports, use one of the channels described in the next section. Email is available for reporters who require it or who need to send encrypted content.

---

## Reporting a Vulnerability

**Please do not open a public GitHub issue for security vulnerabilities.** Public issues are visible to everyone, including potential attackers, before the vulnerability has been fixed.

Instead, use one of the following private reporting channels:

### Option 1: GitHub Private Vulnerability Reporting (preferred)

GitHub provides a built-in mechanism for private security reports:

1. Go to the Keel repository on GitHub.
2. Click **Security** → **Report a vulnerability**.
3. Fill in the details and submit.

Your report is visible only to repository maintainers. GitHub keeps the report confidential until you and the maintainers agree it is appropriate to disclose.

### Option 2: Email

Send your report to **security@byiq.com**. If the vulnerability is particularly sensitive, request a PGP key exchange in your initial message and we will respond with a public key before you send details.

### What to Include in Your Report

A useful security report includes:

- **Description:** A clear description of the vulnerability and what it allows an attacker to do.
- **Affected component:** Which part of Keel is affected (TLS handling, authn middleware, config loading, etc.).
- **Reproduction steps:** Step-by-step instructions to reproduce the vulnerability. Include code, configuration, and commands where possible.
- **Impact assessment:** Your estimate of the severity (what can an attacker achieve? data exfiltration, authentication bypass, denial of service, remote code execution?).
- **Version(s) affected:** Which versions of Keel you have tested.
- **Suggested fix:** If you have ideas about how to fix the issue, include them. This is optional but helpful.
- **Proof of concept:** A minimal PoC demonstrating the vulnerability. Do not include full exploit code.

---

## Triage and Response Timeline

We take security reports seriously and commit to the following response timeline:

| Milestone | Target Timeline |
|---|---|
| Initial acknowledgement | Within 2 business days of receipt |
| Severity assessment | Within 5 business days |
| Fix development begins | Within 10 business days for Critical/High; 30 days for Medium/Low |
| Patch release | Within 30 days for Critical/High; 90 days for Medium/Low |
| Public disclosure | After patch release, coordinated with the reporter |

For Critical vulnerabilities (CVSS >= 9.0) — for example, an unauthenticated remote code execution or a complete authentication bypass — we will prioritize a hotfix release over the normal release cycle.

**What happens after triage:**
1. We open a private GitHub Security Advisory to track the issue.
2. We coordinate with the reporter on the fix and timeline.
3. We prepare a patch and draft release notes.
4. We release the patch.
5. We publish the Security Advisory with full details.
6. We notify downstream users via the GitHub advisory mechanism.

---

## Severity Scoring

We use the [CVSS v3.1](https://www.first.org/cvss/v3.1/specification-document) scoring system to assess severity:

| CVSS Score | Severity | Expected response |
|---|---|---|
| 9.0–10.0 | Critical | Hotfix release, immediate notification |
| 7.0–8.9 | High | Priority patch in next release or hotfix |
| 4.0–6.9 | Medium | Fix in next regular release |
| 0.1–3.9 | Low | Fix in next regular release or maintenance release |

---

## Supported Versions

We provide security fixes for the following versions:

| Version | Status |
|---|---|
| Latest minor release | Supported — receives security fixes |
| Previous minor release | Supported for 6 months after the next minor release |
| Older versions | Not supported — upgrade required |

**Example:** If the current release is v1.3.0, then v1.2.x is supported until 6 months after v1.3.0 was released. v1.1.x and earlier are not supported.

**Upgrade policy:** We do not backport security fixes to unsupported versions. If you are running an unsupported version, upgrade to the latest supported release.

---

## Coordinated Disclosure

We follow **coordinated disclosure** (also called "responsible disclosure"):

1. Reporter submits a private report.
2. Keel maintainers acknowledge, assess, and fix the vulnerability.
3. Maintainers and reporter agree on a disclosure date (typically the day the patch is released, or up to 90 days after the report, whichever comes first).
4. The patch is released.
5. A public Security Advisory is published with full details.

We request that reporters:
- Do not disclose the vulnerability publicly before the agreed disclosure date.
- Do not use the vulnerability against production systems other than those you own or have explicit permission to test.
- Act in good faith — the goal is to protect users, not to harm them.

We commit that Keel maintainers will:
- Act in good faith to fix the issue promptly.
- Credit the reporter in the Security Advisory (unless the reporter prefers to remain anonymous).
- Not pursue legal action against reporters who follow this policy.

---

## Vulnerability Handling: CVE Policy

### CVE Issuance

For confirmed vulnerabilities of Medium severity or higher, we will obtain a CVE (Common Vulnerabilities and Exposures) identifier from a CVE Numbering Authority (CNA). GitHub is a CNA and handles this automatically for vulnerabilities reported via GitHub's private security advisory mechanism.

The CVE will be included in the patch release's CHANGELOG and the published Security Advisory.

### CHANGELOG Marking

Security fixes are marked clearly in the CHANGELOG:

```
## v1.3.1 — 2026-03-15

### Security

- **[CVE-2026-XXXXX]** Fix: authentication bypass when `trusted_ids` list contains an empty string.
  Severity: High (CVSS 8.1). Affected versions: v1.0.0–v1.3.0.
  Credit: reported by Jane Doe.
```

### SBOM and Provenance

Keel's CI pipeline generates a Software Bill of Materials (SBOM) for each release:
- **Format:** SPDX 2.3 (JSON) and CycloneDX 1.4 (XML).
- **Attachment:** SBOM files are attached to each GitHub Release as assets.
- **Provenance:** SLSA Level 2 provenance attestations are generated by the CI pipeline and attached to the release, allowing you to verify that the released binary was built from the published source code.

To verify the provenance of a Keel release:
```sh
# Using the SLSA verifier (https://github.com/slsa-framework/slsa-verifier)
slsa-verifier verify-artifact keel-linux-amd64 \
  --provenance-path keel-linux-amd64.intoto.jsonl \
  --source-uri github.com/keelcore/keel \
  --source-tag v1.3.1
```

---

## Dependency Vulnerability Management

Keel vendors its dependencies (`vendor/` directory is committed). This means:
- Keel is not affected by supply-chain attacks that modify dependencies after-the-fact on a package registry.
- Security fixes to dependencies require explicit updates to the vendor directory.

We scan the vendor directory for known vulnerabilities using `govulncheck` in CI. If a vulnerability is found in a vendored dependency:
1. We update the dependency to a patched version.
2. If no patched version is available, we evaluate workarounds or mitigation.
3. We release a patch and note the dependency CVE in the CHANGELOG.

**Checking your own build for vulnerabilities:**
```sh
# Install govulncheck
go install golang.org/x/vuln/cmd/govulncheck@latest

# Scan your Keel build
govulncheck ./...
```

---

## Security Design Principles

These principles guide Keel's security architecture:

1. **Fail closed:** When in doubt, deny. A misconfigured Keel should refuse to start rather than start in an insecure state.
2. **Minimal attack surface:** Build-time opt-out tags allow removing features entirely from the binary, eliminating their attack surface.
3. **Defense in depth:** OWASP headers, size limits, authn, TLS 1.3 only — each layer protects against a different class of attack.
4. **Conservative defaults:** Every security feature is on by default. You opt out; you do not opt in.
5. **Separation of concerns:** Secrets never live in the primary config file. The secrets file is separate, delivered separately, never committed to source control.
6. **Auditable:** Structured JSON access logs, trace IDs, and Prometheus metrics make security events visible and queryable.
