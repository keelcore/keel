# Runbook: Remote Log Sink Down

## Symptoms

- `keel_log_drops_total` rises, or `remote_sink_init_failed` appears in the logs.
- Logs are missing in the aggregator while the container's stdout has them.

## Immediate triage

Read the drop counter:

```sh
curl -s -H "Authorization: Bearer ${KEEL_ADMIN_TOKEN}" http://127.0.0.1:9999/metrics
```

## Root causes

- The aggregator is down, or `logging.remote_sink.endpoint` is wrong.
- An endpoint limited to TLS 1.2: Keel's outbound policy is TLS 1.3 only.
- Log volume exceeds what the sink accepts.

## Resolution

Restore the aggregator, or correct the endpoint and reload. Requests are unaffected meanwhile: the sink never blocks
them, and stdout keeps every line.

```sh
kill -HUP "${KEEL_PID}"
```

## Escalation

If a compliance requirement depends on the remote copy, recover the gap from container logs, and notify the compliance
owner.
