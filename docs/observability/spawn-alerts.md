# Spawn and session alerts

`loom serve` exposes four families on `/metrics`, produced by
`internal/metrics/agentmetrics`:

| family | type | labels | source |
| --- | --- | --- | --- |
| `loom_agent_spawns_total` | counter | `role`, `status`, `error_class` | the daemon's spawn snapshot (`spawn-metrics.json`) |
| `loom_last_successful_spawn_timestamp_seconds` | gauge | — | the same snapshot |
| `loom_agent_sessions_total` | counter | `role`, `phase`, `status` | `sessions/index.jsonl` |
| `loom_agent_session_duration_seconds` | histogram | `role`, `phase` | `sessions/index.jsonl` |

Spawns are counted in the `loom daemon` process and read back in `loom serve`,
which is why they travel through a file. The gauge is emitted only after a real
successful spawn: a zero would render as 1970 and fire the outage alert
permanently on a fresh install.

## Rules

```yaml
- alert: LoomSpawnOutage
  expr: (time() - loom_last_successful_spawn_timestamp_seconds > 3600)
        or absent(loom_last_successful_spawn_timestamp_seconds)
  for: 10m
- alert: LoomSpawnFailureRate
  expr: sum(rate(loom_agent_spawns_total{status="failure"}[15m]))
        / clamp_min(sum(rate(loom_agent_spawns_total[15m])), 0.001) > 0.5
  for: 15m
```

The `absent()` arm is load-bearing. Without it, "the daemon never came up at
all" is a silent failure of the alert itself — there is no series to compare
against `time()` — and it is also what covers a fresh workspace where the
family is genuinely absent from the wire.

`clamp_min` keeps the ratio defined when no spawns happened in the window: an
idle fleet divides by zero otherwise, and the alert would flap on `NaN`.

## Reading a firing alert

`error_class` is a bounded allowlist (`internal/metrics/spawnmetrics`), so the
failing series names the stage: `materialize_skills`, `build_command`,
`backend_unavailable`, `start`, or `unknown` for anything outside it. A large
`materialize_skills` count with no `status="success"` series at all is the
shape of a full spawn outage.
