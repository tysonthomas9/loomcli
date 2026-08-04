# The Two Session Stores

> **Status:** Current · *audited 2026-07-23*

**Last updated:** 2026-07-23
**Related:** [`session-artifact-contract.md`](session-artifact-contract.md),
[`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md),
[`error-class-reference.md`](error-class-reference.md)

## Purpose

"Session" names two different records in this codebase. They have different
field sets, different status enums, and different writers. Conflating them
produces exactly the confusion
[`agent-execution-prd.md`](agent-execution-prd.md) was written to solve: a task
that is visibly claimed while the UI shows no session.

| | Filesystem session | Control-plane session |
|---|---|---|
| Go type | `sessions.SessionRecord` / `sessions.SessionMetadata` | `domain.AgentSession` |
| Declared at | `internal/sessions/types.go:30-75` / `:79-82` | `internal/domain/control_plane.go:81-102` |
| Lives in | `<runtimeDir>/sessions/` on the runner's disk | fleet-db |
| Store type | `sessions.Store` (`internal/sessions/store.go:28`), rooted by `NewStore` (`store.go:47`) | `store.AgentSessionStore` over HTTP (`internal/infra/fleetdb/control_plane.go:89-212`) |
| Wire | none — local files | `/api/v1/{ws}/agent-sessions` |
| Status enum | `sessions.SessionStatus` (4 values) | `domain.AgentSessionStatus` (10 values) |

## Filesystem session

`sessions.NewStore(runtimeDir)` creates `<runtimeDir>/sessions/`
(`internal/sessions/store.go:45-56`). Each session gets its own subdirectory,
`<runtimeDir>/sessions/<sessionID>/`, holding `prompt.txt`, `metadata.json`,
the native transcript, and the event store's `events.jsonl`
(`store.go:35-42`). A flat `index.jsonl` at the store root carries one
`SessionRecord` per run as the queryable index
(`internal/sessions/types.go:28`, `internal/sessions/compact.go:22`).

`SessionRecord` is the only place token usage and diff stats are recorded per
run: `input_tokens`, `output_tokens`, `cache_read_tokens`,
`cache_write_tokens`, `estimated_cost_usd`, `files_changed`, `lines_added`,
`lines_removed`, `files_touched`, `attempt_num`, `error_class`
(`internal/sessions/types.go:59-74`). It also carries `transcript_format`
(`raw` | `canonical`) so transcript loading dispatches deterministically
rather than guessing (`types.go:19-26,44-48`).

Retention and self-healing are local concerns:

- `Store.PurgeOlderThan` (`internal/sessions/purge.go:15`) behind
  `loom cleanup sessions clean` (`internal/cli/cleanup/sessions_cmd.go:18,26`).
- Records still `running` with no `EndedAt` after
  `StaleSessionThreshold = 4h` are rewritten to `aborted` with
  `EndedAt = StartedAt + 4h` (`internal/sessions/stale.go:12,24-38`). The heal
  is one-way; a runner that resumes does not get its `running` status back.

## Control-plane session

`domain.AgentSession` is the fleet-db record. It carries the fields the UI and
the distributed control plane need — `node_id`, `kind`, `parent_session_id`,
`phase`, `attempt`, `last_heartbeat`, `summary`, `error_class`, `exit_code`,
and a free-form `metadata map[string]string`
(`internal/domain/control_plane.go:81-102`). It carries **no** token usage and
**no** diff stats.

Transport is `internal/infra/fleetdb/control_plane.go`:

| Operation | Route |
|---|---|
| Create | `POST /api/v1/{ws}/agent-sessions` (`:93-124`) |
| Get | `GET /api/v1/{ws}/agent-sessions/{id}` (`:126`) |
| List | `GET /api/v1/{ws}/agent-sessions` (`:134`) |
| Heartbeat | `POST /api/v1/{ws}/agent-sessions/{id}/heartbeat` (`:198`) |
| Update | `PATCH /api/v1/{ws}/agent-sessions/{id}` (`:206`) |

`agent_id` is the worktree name, not a UUID
(`internal/cli/daemon/supervisor/supervisor.go:527`).

**Server-side filters are `agent_id`, `node_id`, `task_id`, `status` only.**
fleet-db's `listAgentSessions` does not accept `kind` or `parent_session_id`;
loom requests the broader set and filters client-side, applying `limit` after
the filter so a page full of non-matching rows cannot silently truncate the
result (`internal/infra/fleetdb/control_plane.go:148-173`,
`filterAgentSessionsClientSide` at `:178-196`).

PATCH bodies are built field-by-field and omit anything unset
(`agentSessionUpdateBody`, `internal/infra/fleetdb/control_plane.go:457-490`).
Public snake_case names only; a null for an unset field is not equivalent.

## Who writes which

| Writer | Filesystem | Control plane |
|---|---|---|
| `loom plan` | yes (`internal/cli/agent/plan.go:245,263-274`) | no |
| `loom task` | yes (`internal/cli/agent/task.go:147` → same `adoptOrCreateSession`) | no |
| daemon supervisor | yes (`supervisor.go:483,497`) | yes (`supervisor.go:524`, kind `task`) |
| `loom lead` | no | yes (`internal/cli/agent/lead/lead.go:337`, kind `orchestration`) |
| driver task bridge | no | yes (`internal/driver/task_bridge_session.go:164`, kind `task`) |
| web UI agent terminal | no | yes (`internal/webui/handlers/terminal/agent_session.go:447`) |
| `loom daemon seed-transcript` | no | yes (`internal/cli/daemon/seed_transcript_cmd.go:74`) |

The first two rows are the operational consequence: **a direct `loom plan` or
`loom task` run publishes no control-plane session at all.** Its evidence
exists only under `<runtimeDir>/sessions/` on the machine that ran it, so a
task claimed by that run shows an empty Sessions tab in any UI reading
fleet-db.

## Rules of thumb

- Reaching for token counts, cost, or diff stats → filesystem record.
- Reaching for lead attribution, node identity, heartbeat, or anything a
  second machine must see → control-plane record.
- Never copy a status value from one enum into the other. See
  [`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md).

## Related

- [`session-artifact-contract.md`](session-artifact-contract.md) — the evidence contract built on both stores
- [`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md) — the two status enums
- [`error-class-reference.md`](error-class-reference.md) — what lands in `error_class`
