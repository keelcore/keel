# Runbook: Log Rotation

## Symptoms

- `logging.file` grows without bound, or the disk fills.
- `log_rotation_failed` or `log_file_open_failed` in the logs, or `log rotation failed` on stderr.

## Immediate triage

Force a rotation, and look for `log_rotated` on stdout:

```sh
kill -USR2 "${KEEL_PID}"
```

## Root causes

- The volume is full or read-only, or the directory is missing.
- `logging.rotation` limits are 0 (off), or too generous for the volume.

## Resolution

Free space, or tighten `logging.rotation.max_size_mb`, `max_backups` and `max_age_days`, then reload. A new policy
applies to the open file:

```sh
kill -HUP "${KEEL_PID}"
```

## Escalation

If the volume cannot be recovered, clear `logging.file` and reload: stdout keeps every line. Notify the platform owner.
