# Runbook: HTTP/3 Falling Back to HTTP/2

## Symptoms

- Clients stay on HTTP/2 or HTTP/1.1 with `listeners.h3` enabled.
- `listener_failed` for the H3 listener, or `h3_no_tls_cert` at startup.

## Immediate triage

Confirm HTTPS responses advertise HTTP/3 in `Alt-Svc`:

```sh
curl -s -o /dev/null -D - https://127.0.0.1:8443/
```

## Root causes

- UDP on the H3 port is blocked by a firewall, security group or load balancer.
- The path MTU is below what QUIC needs (1280 bytes of UDP payload).
- `security.alt_svc_max_age` is 0, or clients cached an older answer.

## Resolution

Open UDP on the H3 port end to end, and raise the path MTU where it is below 1350. Clients fall back to TCP safely, so
this is not an outage.

## Escalation

If `security.min_http_version` is `3`, clients without HTTP/3 are rejected with `KEEL-PROTO-007`: lower the minimum until
the path is fixed, and notify the service owner.
