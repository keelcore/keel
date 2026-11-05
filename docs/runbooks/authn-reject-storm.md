# Runbook: Authentication Reject Storm

## Symptoms

- A spike of 401 or 403 responses in `keel_requests_total`, and `authn_fail` in the logs.
- `trusted_signers_file_read_failed`, `trusted_signers_file_open_failed` after a reload.

## Immediate triage

Compare the reject rate with the time of the last deploy, key rotation or reload:

```sh
curl -s -H "Authorization: Bearer ${KEEL_ADMIN_TOKEN}" http://127.0.0.1:9999/metrics
```

## Root causes

- A signer rotated its key, and `authn.trusted_signers_file` or the JWKS has not caught up.
- A new `sub` is missing from `authn.trusted_ids`, or the audience changed.
- JWKS unreachable with no last good keys (after a restart during an issuer outage).
- Clock skew makes tokens look expired.

## Resolution

Update the trusted signers or IDs, then reload. The signers file is re-read and takes effect at once:

```sh
kill -HUP "${KEEL_PID}"
```

## Escalation

If tokens are being rejected for a signer you did not change, treat it as a possible attack, and page the security
owner.
