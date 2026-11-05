# Runbook: OPA or External Authorization Unavailable

## Symptoms

- A spike of 403 responses (`fail_open: false`), and `authz_call_fail` in the logs.
- `authz_opa_parse_fail` when OPA answers with something unexpected.

## Immediate triage

Confirm Keel itself is healthy, so the failure is on the authorization path:

```sh
curl -s http://127.0.0.1:9091/healthz
```

Then check the OPA process and the `ext_authz.endpoint` path or socket from the same pod.

## Root causes

- OPA crashed, is restarting, or is over `ext_authz.timeout`.
- The socket path or endpoint changed.
- A policy bundle returns a non-boolean `result`.

## Resolution

Restore OPA or correct `ext_authz.endpoint`, then reload:

```sh
kill -HUP "${KEEL_PID}"
```

## Escalation

`fail_open: true` lets traffic through without authorization. Use it only with the security owner's sign-off, and
record it as an incident.
