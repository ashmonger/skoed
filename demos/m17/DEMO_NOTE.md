# M17 Demo Note — Schedule Bindings + Config Shadow Export

## Implemented scope

### GET /api/v1/schedules/{id}/bindings

Returns the list of `{schedule_id, profile_id, blocklist_id}` bindings for a given schedule.

```
# Create a schedule
curl -s -X POST http://localhost:8080/api/v1/schedules \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"id":"weekday","name":"Weekdays","mode":"active","windows":[]}' | jq

# Add a binding
curl -s -X POST http://localhost:8080/api/v1/schedules/weekday/bindings \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"profile_id":"kids","blocklist_id":"social"}' | jq

# List bindings
curl -s http://localhost:8080/api/v1/schedules/weekday/bindings \
  -H "Authorization: Bearer <token>" | jq
# → [{"schedule_id":"weekday","profile_id":"kids","blocklist_id":"social"}]

# Non-existent schedule → 404
curl -s -o /dev/null -w "%{http_code}" \
  http://localhost:8080/api/v1/schedules/missing/bindings \
  -H "Authorization: Bearer <token>"
# → 404
```

### Shadow config.yaml includes schedules and bindings

After any schedule or binding mutation, the ShadowWriter writes `<data_dir>/config.yaml` within ~1 s. The YAML now includes:

```yaml
schedules:
  - id: weekday
    name: Weekdays
    mode: active
    windows: []

schedule_bindings:
  - schedule_id: weekday
    profile_id: kids
    blocklist_id: social
```

This makes PBS/restic filesystem-level backups of the data directory carry a complete, human-readable snapshot of all cluster-replicated state.

## Not implemented in M17

- UI for schedule binding management (the existing schedule builder handles windows only)
- Bulk-binding multiple (profile, blocklist) pairs in one request
- Binding overlap / conflict validation

## Limitations

- `GET /api/v1/schedules/{id}/bindings` is always served from local node state; in a cluster with a very recent mutation, there is a sub-second window where followers may return slightly stale data (normal Raft eventual consistency).
- `schedule_bindings:` is omitted from the shadow YAML when the list is empty (uses `omitempty`).

## Acceptance tests

4 tests added to `tests/acceptance/schedules_test.go`:
- `TestScheduleBindingsList` (FS-ScheduleBindingsList)
- `TestScheduleBindingsListEmpty` (FS-ScheduleBindingsListEmpty)
- `TestScheduleBindingsListNotFound` (FS-ScheduleBindingsListNotFound)
- `TestScheduleWrittenToConfigYaml` (FS-ScheduleConfigYaml)

All 4 green. Full suite: 413 tests green.
