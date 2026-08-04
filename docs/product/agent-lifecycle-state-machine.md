# Agent Lifecycle State Machine

> **Status:** Current · *audited 2026-08-03*. The tables below were rewritten
> from the shipped enums. The 2026-05-04 draft's state machines were never
> built as drafted and are preserved under "Proposed, not implemented" — but
> that section records unbuilt *diagrams*, not forbidden words. Of the draft's
> vocabulary, only `preflight`, `claimed_task`, `finalizing`, `registered`,
> `stopping`, `enabled`, and `stale` are absent from the tree; `starting` and
> `blocked` shipped (`domain.AgentSessionStarting`, `entity.StatusBlocked`),
> and `disabled`/`error` are live values in unrelated domains
> (`domain.ConnectorStatusDisabled`, `domain.WorkspaceStateError`).

**Last updated:** 2026-08-03
**Related:** [`session-stores.md`](session-stores.md),
[`error-class-reference.md`](error-class-reference.md),
[`session-artifact-contract.md`](session-artifact-contract.md),
[`agent-run-ux-spec.md`](agent-run-ux-spec.md)

## Purpose

This document is the single place agent, session, and task state vocabularies
are defined. Other product docs link here rather than restating them.

Three separate state machines exist, and they are not layers of one:

- **agent state** — the long-lived configured assignment
- **session state** — one execution attempt, in two different stores
- **task state** — the issue lifecycle

Error classes are **not** states. They live in
[`error-class-reference.md`](error-class-reference.md).

## Agent States

Three distinct fields on `domain.Agent` (`internal/domain/agent.go:42`), plus a
fourth, non-`domain` status string produced by the local daemon. They answer
different questions and must not be collapsed.

### `domain.AgentState` — stored intent

`internal/domain/agent.go:8-20`.

| Value | Meaning |
|---|---|
| `idle` | Registered, not currently active. |
| `active` | Running. |
| `stopped` | Intentionally stopped. |
| `backend_unavailable` | Registered, but the backend CLI it would invoke is not on PATH. The daemon reconciler re-checks PATH each tick and auto-transitions back to `idle` once the binary appears (`agent.go:14-19`). |

This is loom's view of the assignment, deliberately distinct from fleet-db's
per-claim Worker state (`agent.go:5-7`).

### `domain.AgentDesiredState` — reconciler target

`internal/domain/control_plane.go:12-19`.

| Value | Meaning |
|---|---|
| `stopped` | Should not be running. |
| `idle` | Should exist but hold no work. |
| `running` | Should be running. |
| `draining` | Should finish current work and stop taking more. |

### `domain.AgentLiveStatus` — derived, read-only

`internal/domain/agent.go:22-32`. Values: `working`, `idle`.

fleet-db computes this from its session+lease join and returns it on the agent
response. **loom never derives it** — it passes it straight through
(`agent.go:22-25`). Do not compute or write it client-side.

### Daemon agent status — derived locally, not a `domain` enum

An untyped string on `DaemonAgentStatus.Status`
(`internal/cli/daemon/daemon_cmd.go:31`), computed by `computeAgentStatus`
(`internal/cli/daemon/daemon_state.go:162-185`) from the supervisor's PID and
`StopReason`, persisted to `daemon-agents.json` (`writeStateFile` at
`daemon_state.go:95`, field set at `:131`), and rendered by `loom daemon status`
and the web UI
(`internal/cli/serve/daemonwire/daemon.go:138`).

| Value | Meaning |
|---|---|
| `running` | PID is live (`daemon_state.go:163-165`). |
| `blocked` | Restart budget exhausted but the policy blocks-and-retries; the supervise goroutine is alive and self-resumes (`StopReasonMaxRetriesBlocked`, `daemon_state.go:170-171`). |
| `failed` | `StopReasonFatalError`, `StopReasonMaxRetries`, `StopReasonFastFail`, or restarts over budget (`daemon_state.go:175-182`). |
| `stopped` | Everything else (`daemon_state.go:184`). |

This is **not** `domain.AgentState`: `blocked` and `failed` are not
`AgentState` values, and nothing here is persisted to fleet-db. The struct's
comment also declares `starting`, but `computeAgentStatus` never returns it —
treat that value as dead in this field. `blocked` here is unrelated to
`entity.IssueStatus` `blocked` under "Task States"; they collide by name only.

## Session Statuses

Two enums for two stores. See [`session-stores.md`](session-stores.md) for
which writers produce which record.

### `domain.AgentSessionStatus` — control-plane record

`internal/domain/control_plane.go:66-79`.

| Value | Terminal | Meaning |
|---|---|---|
| `queued` | no | Requested, not started. |
| `leased` | no | A lease has been taken for it. |
| `starting` | no | Runtime/process is starting. |
| `running` | no | Active. |
| `idle` | no | Alive but not working. |
| `yielded` | no | Gave up its slot. |
| `completed` | yes | Finished successfully. |
| `failed` | yes | Finished with failure. |
| `cancelled` | yes | Cancelled. |
| `expired` | yes | Lease/lifetime expired. |

### `sessions.SessionStatus` — filesystem record

`internal/sessions/types.go:6-13`. A different, smaller enum.

| Value | Terminal | Meaning |
|---|---|---|
| `running` | no | Active. |
| `completed` | yes | Finished successfully. |
| `failed` | yes | Finished with failure. |
| `aborted` | yes | Stopped before completion, including the 4h stale heal (`internal/sessions/stale.go:12,24-38`). |

`aborted` exists only on the filesystem side; `leased`, `idle`, `yielded`,
`cancelled`, and `expired` exist only on the control-plane side. Nothing maps
between them automatically.

### `domain.AgentSessionKind`

`internal/domain/control_plane.go:56-64`: `task`, `orchestration`, `terminal`,
`maintenance`, `ad_hoc`. There is no `review` kind — the PR reviewer is a
persisted agent, not a session kind
(see [`pr-review-spec.md`](pr-review-spec.md) §3).

## Stop Reasons

"Why did the agent stop" is a separate vocabulary from both status and error
class: `supervisor.StopReason`
(`internal/cli/daemon/supervisor/types.go:72-98`). Values and meanings are
tabulated in
[`error-class-reference.md`](error-class-reference.md#2-supervisorstopreason--why-the-agent-loop-stopped);
they include the successful-exit reasons `no_work`, `yielded`, and
`ephemeral_done`, so a stop reason is not evidence of failure.

## Task States

`entity.IssueStatus` (`internal/entity/issue.go:225-238`). All nine are
accepted by `IsValid`, along with the empty string, which the caller defaults
(`issue.go:242-249`).

| Value | Meaning |
|---|---|
| `open` | Available for planning or implementation. |
| `in_progress` | Claimed by an agent or user. |
| `blocked` | Cannot proceed until a blocker is resolved. |
| `deferred` | Intentionally postponed. |
| `review` | Awaits review. |
| `closed` | Completed. |
| `tombstone` | Deleted/retired marker. |
| `pinned` | Pinned. |
| `hooked` | Hooked. |

Workspaces may also define custom statuses, validated by
`IssueStatus.IsValidWithCustom` (`internal/entity/issue.go:252`).

### Task transitions

The FleetBackend does **not** enforce a source→target transition graph:
`applyStatusUpdate` switches on the *target* status and accepts it largely
independent of the current status
(`internal/backend/fleet/fleet.go:640-687`). For example `open -> review` and
`open -> blocked` are both accepted, not only the `in_progress ->` paths
(`TestUpdate_ReviewFromOpen_NoRelease`,
`TestUpdate_QuarantineShape_OpenToBlockedWithLabelAndUnassign`,
`internal/backend/fleet/fleet_release_test.go:347,397`). The one released side
effect is that entering `blocked`/`review` from `in_progress` drops the claim
(`fleet.go:725-735`, `TestUpdate_ReviewOrBlockedFromInProgress_ReleasesClaim`,
`fleet_release_test.go:219`).

The paths below are the **common workflow moves, not an exhaustive graph**:

```text
open -> in_progress
in_progress -> review
in_progress -> blocked
in_progress -> closed
review -> open
blocked -> open
```

## State Invariants

Verified in code:

- The control-plane record has `finished_at` and `exit_code` fields for
  terminal sessions; both are pointers and are omitted when unset
  (`internal/domain/control_plane.go:95,98`).
- A filesystem session left `running` with no `ended_at` for 4h is rewritten
  to `aborted` (`internal/sessions/stale.go:12,24-38`). The rewrite is
  one-way and destructive; it is not reversible if the runner resumes.

Not enforced, despite earlier drafts claiming otherwise:

- Nothing requires a `failed` session to carry `summary` or `error_class`.
  Both are optional pointers on `AgentSessionUpdate` and are omitted when
  unset (`internal/infra/fleetdb/control_plane.go:477-482`).

## Proposed, not implemented

Kept as design history. Neither machine below was built as drawn: there is no
run object and no agent-definition enum. Do not implement these diagrams — but
do **not** read the lists as reserved words either. Several of the individual
names are live enum values in the tables above (`queued`, `starting`,
`running`, `idle`, `completed`, `failed` on `domain.AgentSessionStatus`;
`aborted` on `sessions.SessionStatus`; `blocked` on `entity.IssueStatus` and in
the daemon status string; `stopped` on `domain.AgentState`). Write code against
the tables, not against these diagrams. Only `preflight`, `claimed_task`,
`finalizing`, `registered`, `stopping`, `enabled`, and `stale` are absent from
the tree entirely; `disabled` and `error` exist, but for connectors, drivers,
and workspaces — never for agents.

The 2026-05-04 draft proposed a **run** object with states `queued`,
`starting`, `preflight`, `running`, `claimed_task`, `finalizing`, `completed`,
`failed`, `blocked`, `aborted`, `stale`, and this transition diagram:

```text
queued -> starting -> preflight -> running
running -> claimed_task
claimed_task -> finalizing
finalizing -> completed | failed | blocked
running -> failed | aborted | stale
stale -> failed
```

It also proposed an agent-definition machine over `registered`, `enabled`,
`disabled`, `starting`, `idle`, `running`, `stopping`, `stopped`, `stale`,
`error`.

What replaced them, and why the substitution is not mechanical:

- There is no run object. The execution attempt is the **session**, and
  `session_id` is its identity.
- `preflight` is not a state because preflight runs *before* anything is
  created: `PreflightLocalTaskRunner` fails closed and the run is never queued
  (`internal/runtimepreflight/preflight.go:69-77`).
- `claimed_task` and `finalizing` are phases within `running`, carried by the
  free-form `phase` field (`internal/domain/control_plane.go:91`), not by
  status.
- `stale` is not a state anywhere. It is an observation, and it materializes
  as the destructive `aborted` heal above.
- The agent-definition machine collapsed into three orthogonal fields
  (stored/desired/live) rather than one enum.

## Acceptance Criteria

- UI and API use the enum values above verbatim, per store.
- Task state and session state are never conflated in one column.
- A stale/expired session is displayed differently from a running one.
- Error classes are never rendered in a status column.

## Related

- [`session-stores.md`](session-stores.md) — the two session records and their writers
- [`error-class-reference.md`](error-class-reference.md) — failure vocabularies
- [`session-artifact-contract.md`](session-artifact-contract.md) — the evidence contract
- [`agent-run-ux-spec.md`](agent-run-ux-spec.md) — how these states surface in the UI
- [`failure-modes-recovery-ux.md`](failure-modes-recovery-ux.md) — recovery behavior
