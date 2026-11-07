# Threat Model

**Scope:** Keel as a sidecar, Keel as a library, and the build and release pipeline, including the tool images and
signing. Data flows are in [architecture.md](architecture.md). Fit and non-goals are in [impact.md](impact.md).

**Review cadence:** each MINOR release, and whenever Keel gains an external interface (a new listener, outbound
client or credential). The release how-to in [release-policy.md](release-policy.md) carries this as a checklist item.

**Method:** STRIDE per component. Likelihood and impact are Low, Medium or High, for a deployment that follows
[security.md](security.md). Residual is what remains after the mitigation.

## Threats

| ID | Component | Threat | Likelihood | Impact | Mitigation | Residual |
| --- | --- | --- | --- | --- | --- | --- |
| TLS-1 | TLS | Tampering: downgrade to a weak protocol | Low | High | One policy, TLS 1.3 only, for listeners and clients (`pkg/core/tlspolicy`, `TestOutboundTLSPolicy_NoOtherPath`) | Clients below 1.3 are refused and need an upgrade |
| TLS-2 | TLS | Spoofing: a stolen or expired server key | Low | High | Key files read-only in the pod; `keel_tls_cert_expiry_seconds`; ACME renewal 30 days before expiry | A stolen key is valid until revoked or expired |
| AUTHN-1 | Authn | Spoofing: forged JWT or algorithm confusion | Low | High | Signature, `exp` and audience enforced; PEM never treated as an HMAC secret (`TestAuthnJWT_*`) | Trust in the configured signers |
| AUTHN-2 | Authn | Denial of service: JWKS outage or fetch flood | Medium | Medium | Last good keys served through a failure backoff; one fetch per URL; response size capped | New signers wait for recovery |
| AUTHN-3 | Authn | Spoofing: credential divergence between Keel and upstream | Low | High | Two `Authorization` lines are refused; with signing on, only Keel's token goes upstream (`TestHopHeaders_*`) | With signing off, the upstream validates the client's token itself |
| AUTHZ-1 | Authz | Elevation: policy bypass while OPA is down | Medium | High | `fail_open: false` by default; 3xx is not an allow | `fail_open: true` is an operator choice to accept this |
| AUTHZ-2 | Authz | Tampering: smuggled values in policy input | Low | Medium | Each header line is its own array element; JSON encoding (`TestAuthzPayload_ClientValuesStayOneElement`) | Policy quality is the operator's |
| ACME-1 | ACME | Spoofing: a Host header steers redirects or challenges | Low | Medium | The port-80 redirect only targets configured domains; certificate SANs validated on load | Port 80 must stay reachable |
| ACME-2 | ACME | Information disclosure: cached private key | Low | High | Cache files written owner-only; dedicated volume (`cache_dir`) | Node-level access to the volume |
| AUTHN-4 | Authn | Spoofing: OIDC login forgery, code interception or a forged session | Low | High | `state`, `nonce` and PKCE on each login; ID token signature, issuer and audience; AES-GCM sealed cookies (`TestOIDC_*`, `TestCompose_OIDCLoginWithDex`) | A copied session cookie stays valid until it expires |
| PROXY-1 | Proxy | Spoofing: client-supplied hop headers | Medium | High | Hop headers replaced or stripped, X-Forwarded-For normalized (`docs/security.md` §5.6) | Earlier `Via` entries are unverified claims until RFC 9421 signing (#34) |
| PROXY-2 | Proxy | Denial of service: slow or broken upstream | Medium | Medium | Header deadline, circuit breaker, response size cap, memory shedding, bounded retries | Capacity limits still apply |
| PROXY-3 | Proxy | Tampering: request replay on retry | Low | High | Failed dials only for non-idempotent methods; `Retry-After` retried once for replayable requests (`TestRetryAfter_*`) | Idempotency of the upstream's own handlers |
| ADMIN-1 | Admin | Information disclosure: metrics or pprof exposed | Medium | Medium | Separate listener, off by default; bearer tokens; pprof opt-in and compiled out by `no_pprof` | A leaked admin token until rotated |
| LOG-1 | Remote log sink | Information disclosure: sensitive data in logs | Low | Medium | Secrets redacted; client values encoded per destination; TLS through the policy | Upstream paths appear in access logs |
| LOG-2 | Remote log sink | Repudiation: forged log lines or dropped logs | Low | Medium | Control characters escaped, one line per entry; drops counted (`keel_log_drops_total`) | Whole-file rewrite on one host, without `audit.remote_sink` |
| FIPS-1 | FIPS monitor | Tampering: running outside FIPS mode unnoticed | Low | High | Startup gate and background monitor; `keel_fips_active`, `/version` | Module validation scope is the toolchain's |
| REL-1 | Release pipeline | Tampering: compromised dependency or action | Low | High | Actions pinned by SHA; vendored Go modules; `update-pins` verification; govulncheck, Grype, gosec, CodeQL, Scorecard | A compromise upstream of a pinned, reviewed version |
| REL-2 | Release pipeline | Spoofing: forged release artifact | Low | High | Keyless signing, SLSA provenance, SBOMs, checksums; self-verification before publish (`make verify-release`) | Trust in GitHub OIDC and Sigstore |
| REL-3 | Release pipeline | Tampering: unauthorized change reaches `main` or a tag | Low | High | Rulesets as code, signed commits and tags, required checks (`docs/governance.md`) | Admin bypass, recorded per the bypass policy |
| REL-4 | Release pipeline | Tampering: a compromised tool image | Low | High | Tool images pinned by digest, built by CI and signed keyless, built from pinned inputs | Trust in the `.standards` base image |

## Known Limitations

- OIDC sessions are stateless: logout clears the browser's cookie and cannot revoke a copy of it.
- A client's identity is not derived from its mTLS certificate; use JWT authentication for identity.
- `Via` entries from earlier hops are unverified. Signed hop provenance is issue #34.
- Keel is not a WAF: it does not inspect request bodies for attacks ([impact.md](impact.md)).
