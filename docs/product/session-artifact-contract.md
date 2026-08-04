# Session and Artifact Contract

> **Status:** Partially implemented · *audited 2026-07-23*. The metadata,
> token-usage, PATCH, and ephemeral-attempt sections describe shipped
> behavior. The cleanup-metadata and artifact sections are still largely
> aspirational and are marked inline.

**Last updated:** 2026-07-23
**Related:** [`session-stores.md`](session-stores.md),
[`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md),
[`error-class-reference.md`](error-class-reference.md),
[`agent-run-ux-spec.md`](agent-run-ux-spec.md),
[`container-runner-mvp-spec.md`](container-runner-mvp-spec.md),
[`lead-agent-epic-runner-spec.md`](lead-agent-epic-runner-spec.md)

## Purpose

Define the product-level contract for what every agent run must leave behind.
This is the canonical home for the session/artifact data contract; other
product docs link here instead of restating field lists.

This contract applies to:

- local daemon agents
- direct CLI runs
- UI-started local runs
- container/remote workers

## Two stores, one contract

"Session" is two records, not one:

- the **filesystem** record, `sessions.SessionRecord` under
  `<runtimeDir>/sessions/` (`internal/sessions/store.go:28,47`,
  `internal/sessions/types.go:30-75`), and
- the **control-plane** record, `domain.AgentSession` in fleet-db
  (`internal/domain/control_plane.go:81-102`), written over
  `/api/v1/{ws}/agent-sessions`.

They have different field sets, different status enums, and different writers.
[`session-stores.md`](session-stores.md) is the full treatment, including the
writer table. The one consequence to keep in mind while reading this document:
`loom plan` and `loom task` write **only** the filesystem record
(`internal/cli/agent/plan.go:245,263-274`), so their evidence is invisible to
anything reading fleet-db.

The unit of one execution attempt is the **session**. There is no `run_id`;
`session_id` is the attempt identity in both stores
(`internal/domain/control_plane.go:83`, `internal/sessions/types.go:35`).
`attempt` / `attempt_num` is a counter within it, not a separate object. The
only `*_run` identifiers in the codebase — `driver_run_id`, `task_run_id` —
belong to the driver subsystem (`internal/agentinbox/message.go:22-23`).

## Contract Summary

Every agent run should produce:

- session metadata
- task association
- transcript or log evidence
- final status
- error class when failed
- artifacts for changed files, tests, commits, and push attempts when
  applicable

## Required Metadata

### Control-plane record — `domain.AgentSession`

Fields as declared at `internal/domain/control_plane.go:81-102`; the create
payload is `internal/infra/fleetdb/control_plane.go:93-124`.

| Field | Required | Notes |
|---|---|---|
| `workspace_key` | yes | Workspace scope. Not `workspace_id`. |
| `session_id` | yes | The attempt identity. |
| `agent_id` | yes | The worktree name, not a UUID (`internal/cli/daemon/supervisor/supervisor.go:527`). |
| `node_id` | no | Node that ran it. |
| `kind` | yes | `task` \| `orchestration` \| `terminal` \| `maintenance` \| `ad_hoc`. |
| `task_id` | no at create, set after claim | Filled when a task is selected. |
| `terminal_id` | no | Set for terminal-backed sessions. |
| `parent_session_id` | no | Lead/orchestrator attribution. Set from `ap.ParentSessionID` at create (`supervisor.go:531`). |
| `status` | yes | See [`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md). |
| `phase` | no | Free-form, e.g. `planning`, `implementation`. |
| `attempt` | no | Attempt counter within the session. |
| `started_at` / `last_heartbeat` | no | Timestamps. |
| `finished_at` | no | Set for terminal statuses. |
| `summary` | no | Human-readable outcome. |
| `error_class` | no | See [`error-class-reference.md`](error-class-reference.md). |
| `exit_code` | no | Pointer; omitted when unset. |
| `metadata` | no | `map[string]string`; see below. |

Known `metadata` keys, all written by the supervisor
(`internal/cli/daemon/supervisor/supervisor.go:620-645`): `backend`,
`epic_id`, `task_id`, `repo`, `transcript_path`, `log_path`, plus
`attempt_kind` and `cleanup_state` for ephemeral-mode agents only.

There is **no** `role` and **no** `runtime_provider` on a session.
`RuntimeProvider` is a `Node` field (`internal/domain/control_plane.go:43`)
with enum `local|e2b|kubernetes|ci|other` (`control_plane.go:21-29`); role is
`domain.Agent.RoleName` (`internal/domain/agent.go:45`).

### Filesystem record — `sessions.SessionRecord`

Fields as declared at `internal/sessions/types.go:30-75`.

| Field | Notes |
|---|---|
| `schema_version` | `CurrentSchemaVersion = 1`; absent means 0 (`types.go:17`). |
| `session_id` | Attempt identity. |
| `task_id` | Populated at Finalize, not at create — the agent claims mid-session (`types.go:36`). |
| `epic_id` | Parent epic. |
| `agent_name`, `backend`, `model`, `phase` | Agent context. |
| `transcript_format` | `raw` \| `canonical`; empty means legacy/raw (`types.go:19-26`). |
| `started_at`, `ended_at`, `duration_s` | Timing. |
| `status`, `exit_code` | Outcome. |
| `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens`, `estimated_cost_usd` | Token usage. |
| `files_changed`, `lines_added`, `lines_removed`, `files_touched` | Diff stats. |
| `attempt_num`, `error_class` | Retry context. |

Token usage and diff stats exist **only** here. The control-plane record
carries neither.

## Transcript

Transcript should capture AI/backend interaction and tool events.

Minimum fields:

- timestamp
- sequence number
- role/source
- event type
- content or tool metadata
- redaction marker when content was filtered

Transcript may be unavailable for some backends, but the run must then
include logs explaining what evidence is available.

## Logs

Logs should capture process and runner output:

- runner startup
- preflight
- task selection
- task claim result
- backend invocation
- command output
- finalization

Logs must be redacted for credentials and secret-looking values.

## Token Usage

`usage.SessionUsage` (`internal/usage/usage.go:14-29`) is the recorded shape:

- `input_tokens`, `output_tokens`
- `cache_read_tokens`, `cache_write_tokens`
- `estimated_cost_usd`
- `model`, plus `agent_name` / `backend` / `task_id` / `epic_id` /
  `session_id` for attribution

Cost is computed by `EstimateCost` from a per-backend `PricingTier`;
`DefaultPricing` currently covers `claude`, `codex`, and `opencode`
(`internal/usage/usage.go:41-56`).

**Limitation, not a requirement.** An earlier draft required missing usage to
be represented as "unknown, not zero". That is not implementable against the
current shape: the `SessionRecord` token fields are non-pointer `int64` with
no `omitempty` (`internal/sessions/types.go:59-64`), so "unreported" and
"zero" are indistinguishable on the wire. Changing this needs a field-shape
change first.

## Diff Artifact

When files change:

- changed file list
- lines added/removed
- patch or diff reference
- binary file marker
- generated/ignored file marker when known

Diff artifacts should be captured before cleanup and before container exit.

## Ephemeral Task Attempt Contract

An ephemeral worker spawned by the epic runner is a single task attempt.

Required behavior:

```text
create session before model invocation
associate exactly one task_id before claim/model work
record lead/orchestrator attribution when present
complete session before worker is hidden from live-agent UI
preserve evidence before worktree/container cleanup
```

Required metadata for an ephemeral task attempt:

- `attempt_kind = ephemeral_task_attempt` — a **key in the `metadata` map**,
  not a column, written only for `domain.AgentModeEphemeral` agents
  (`internal/cli/daemon/supervisor/supervisor.go:632-635`)
- `agent_id` = worker agent name (the worktree name)
- `task_id` = assigned task
- `parent_session_id` when spawned by a lead — this is the lead-attribution
  mechanism; there is no `lead_agent_name` field
  (`internal/domain/control_plane.go:89`)
- `workspace_key`
- `status`
- `started_at`
- `finished_at` for terminal statuses
- `exit_code` or `error_class` for terminal statuses

Ephemeral task attempts must not be reused for another task. A rerun creates a
new session and a new worker attempt identity.

## Cleanup Metadata

> **Aspirational.** Of everything in this section, only `cleanup_state` is
> written today, and only ever with the value `"retained"`, on ephemeral-mode
> agents (`internal/cli/daemon/supervisor/supervisor.go:634`). `archived`,
> `worktree_deleted`, and `artifacts_deleted` are never written by any code
> path. No disk-usage estimate is computed anywhere under `internal/` (there
> is no `DiskUsage` symbol), and there is no cleanup timestamp, cleanup actor,
> or retained-artifact-kind list. The shipped cleanup surface is the CLI:
> `loom cleanup sessions clean` (`internal/cli/cleanup/sessions_cmd.go:18,26`)
> over `Store.PurgeOlderThan` (`internal/sessions/purge.go:15`).

Runs that retain local disk state should report cleanup metadata:

- worktree/container path when server-visible
- disk usage estimate
- retained artifact kinds
- cleanup state: `retained` (shipped), `archived`, `worktree_deleted`,
  `artifacts_deleted` (proposed)
- cleanup timestamp
- cleanup actor

Cleanup is allowed to delete worktrees or containers only after the evidence
needed by the UI has been persisted. At minimum, cleanup must preserve:

- session metadata
- final status
- task association
- lead/orchestrator attribution
- logs or transcript reference
- diff summary or "no diff" marker
- test/gate result when available
- commit/push result when available

## Commit Artifact

When a commit is created:

- commit SHA
- commit message
- branch
- repo
- files included

When push is attempted:

- target remote
- target branch
- success/failure
- error summary

## Test Artifact

When tests or gates run:

- command
- start/end time
- exit code
- summarized output
- full log reference
- pass/fail/skipped state

If no gate command exists:

```text
gate_status = unavailable
reason = no configured gate command
```

## Error Class

`error_class` is a free-form string on both records
(`internal/sessions/types.go:74`, `internal/domain/control_plane.go:97`). The
values that are actually written are enumerated once, in
[`error-class-reference.md`](error-class-reference.md). Do not invent new ones
here.

Summary: the normal agent-exit path writes an `agenterr.Outcome` name
(`AuthFailure`, `RateLimited`, `NoWork`, `SpawnFailure`, …) via
`ap.LastError.Class.String()`
(`internal/cli/daemon/supervisor/session_finalize.go:121,168-176`); two
snake_case exceptions bypass it (`spawn_failure`, `backend_unavailable`); and
driver runs use their own set.

Error classes are not states. See
[`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md).

## Required Terminal States

Terminal statuses differ per store — this is the same split described in
"Two stores, one contract" above.

- Filesystem (`sessions.SessionStatus`, `internal/sessions/types.go:6-13`):
  `completed`, `failed`, `aborted`.
- Control plane (`domain.AgentSessionStatus`,
  `internal/domain/control_plane.go:66-79`): `completed`, `failed`,
  `cancelled`, `expired`.

`blocked` and `stale` are terminal in neither and are not status values at
all. Full tables in
[`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md).

Terminal state should include an explanation suitable for UI display, but
nothing enforces this: `summary` and `error_class` are both optional pointers
on `AgentSessionUpdate` and are omitted when unset
(`internal/infra/fleetdb/control_plane.go:477-482`).

## Storage Requirements

- Artifacts must survive process/container exit.
- UI APIs must be able to list artifacts by task and run.
- Filesystem paths may be stored as local artifact references only when
  the server can read them.
- Distributed/container artifacts should use server-visible storage.
- Deleting an ephemeral worker worktree must not delete run/session metadata or
  artifact references required for history views.

## FleetDB Control-Plane Requirements

Agent sessions are the canonical run-visibility record for local mode and
distributed mode. Runners must create and update them through FleetDB, then
attach artifact references to the same `session_id` and `task_id`.

The minimum field set is the `domain.AgentSession` table above.

### Queries

Server-side filters on `GET /api/v1/{workspace}/agent-sessions` are
`agent_id`, `node_id`, `task_id`, and `status`
(`internal/infra/fleetdb/control_plane.go:134-147`):

```text
GET /api/v1/{workspace}/agent-sessions?task_id={task_id}
GET /api/v1/{workspace}/agent-sessions?agent_id={agent_id}
```

`parent_session_id` and `kind` are **not** server-side. fleet-db's
`listAgentSessions` does not accept them, so loom requests the broader set and
filters client-side, applying `limit` after the filter
(`internal/infra/fleetdb/control_plane.go:148-173`,
`filterAgentSessionsClientSide` at `:178-196`). Treat lead-worker-history
queries as O(workspace sessions) until fleet-db adds the parameter.

These queries back the task Sessions tab and lead worker history without
reconstructing ownership from local filesystem paths.

### PATCH shape

PATCH payloads must use public snake_case JSON field names and omit fields
that are not being changed. This is exactly what `agentSessionUpdateBody`
does (`internal/infra/fleetdb/control_plane.go:457-490`). Uppercase Go struct
field names, or nulls for unset fields, break parity between local and
distributed readers.

## Acceptance Criteria

- Every session has metadata, status, and a `session_id`.
- Every failed session has an error class and user-readable message.
- Every ephemeral worker attempt has exactly one task association.
- Completed ephemeral worker attempts remain queryable after their worktree is
  deleted.
- Cleanup metadata tells the UI whether disk is still retained and which
  cleanup actions are available. *(Not met — see Cleanup Metadata.)*
- Every file-changing run has diff metadata.
- Every test/gate attempt has a recorded command and result.
- Completed artifacts remain available after runner exit.

## Related

- [`session-stores.md`](session-stores.md) — the two session records in detail
- [`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md) — status vocabularies
- [`error-class-reference.md`](error-class-reference.md) — `error_class` values
- [`agent-run-ux-spec.md`](agent-run-ux-spec.md) — how this data is surfaced
- [`agent-execution-prd.md`](agent-execution-prd.md) — why the contract exists
