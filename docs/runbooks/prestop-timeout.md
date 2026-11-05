# Runbook: Prestop or Shutdown Timeout

## Symptoms

- Pods reach `terminationGracePeriodSeconds` and are killed, and clients see connection resets during rollouts.
- `prestop_sleep` and `readiness_draining` in the logs, with no clean exit after them.

## Immediate triage

During a rollout, confirm readiness flips before the drain:

```sh
curl -s http://127.0.0.1:9092/readyz
```

## Root causes

- `terminationGracePeriodSeconds` is below `timeouts.prestop_sleep` plus `timeouts.shutdown_drain`.
- Long-lived requests outlast `timeouts.shutdown_drain`.
- `prestop_sleep` is too short for the load balancer to stop routing.

## Resolution

Set `terminationGracePeriodSeconds` above the sum of the two timeouts, and tune each for the workload.

## Escalation

If drops continue with correct timeouts, capture the rollout timeline with load balancer logs, and involve the platform
owner.
