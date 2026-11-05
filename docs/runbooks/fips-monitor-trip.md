# Runbook: FIPS Monitor Trip

## Symptoms

- `keel_fips_monitor_failures_total` rises, or `keel_fips_active` is 0 on a FIPS build.
- At startup: `fips_monitor_check_failed`, and the process exits.

## Immediate triage

Check the running build and mode:

```sh
curl -s http://127.0.0.1:9999/version
```

## Root causes

- The binary is not the FIPS flavor, or it runs without `GOFIPS140` / `GODEBUG=fips140=only`.
- A deployment changed the image or the environment.

## Resolution

Redeploy the `-fips` image with the FIPS environment set, and confirm `fips_active` in `/version`.

## Escalation

Treat traffic served while the monitor was failing as outside the FIPS boundary. Notify the compliance owner.
