# Runbook: Certificate Expiry

## Symptoms

- `keel_tls_cert_expiry_seconds` falls below 30 days (2592000), or goes negative.
- Clients report certificate errors. Logs show `acme_failed` or `tls_cert_reload_failed`.

## Immediate triage

Read the expiry gauge on the admin port:

```sh
curl -s -H "Authorization: Bearer ${KEEL_ADMIN_TOKEN}" http://127.0.0.1:9999/metrics
```

## Root causes

- ACME: port 80 unreachable from the CA, rate limiting, or an unwritable `tls.acme.cache_dir`.
- Files: the certificate was renewed on disk, and Keel has not reloaded it.

## Resolution

- ACME: restore port 80 reachability, or free the cache volume. Keel retries on its own.
- Files: replace the files, then reload (`kill -HUP` on the Keel process):

```sh
kill -HUP "${KEEL_PID}"
```

## Escalation

If expiry is under 7 days and renewal keeps failing, install a certificate from another CA through `tls.cert_file` and
`tls.key_file`, and page the platform owner.
