# Agent Run UX Spec

> **Status:** Partially implemented · *audited 2026-07-23*. Roughly half of
> this spec is backed by shipped code. Everything under "Proposed — not built"
> describes surfaces that do not exist; sections above it are marked shipped
> or corrected inline.

**Last updated:** 2026-07-23
**Related:** [`agent-execution-prd.md`](agent-execution-prd.md),
[`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md),
[`session-artifact-contract.md`](session-artifact-contract.md),
[`session-stores.md`](session-stores.md),
[`error-class-reference.md`](error-class-reference.md),
[`lead-agent-epic-runner-spec.md`](lead-agent-epic-runner-spec.md)

## Purpose

Define what the UI should show for agent execution across local,
daemon-managed, direct CLI, and container/remote runs.

The UI should answer three questions quickly:

- What is running?
- What task does it own?
- What evidence did it leave behind?

Field lists are not repeated here. The session/artifact data contract is
[`session-artifact-contract.md`](session-artifact-contract.md); state
vocabularies are
[`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md).

## Primary Surfaces

| Surface | Purpose | Status |
|---|---|---|
| Agent sidebar | Current and recent agent/session status. | Shipped |
| Kanban card | Task ownership, state, and latest session signal. | Shipped |
| Issue detail panel | Timeline, sessions, files, logs, and recovery actions. | Shipped |
| Terminal/session view | Live or archived transcript/log stream. | Shipped |
| Lead agent page | Epic-scoped terminal, tasks, live workers, and worker history. | Shipped |
| Run detail panel | Full execution metadata and artifacts for one attempt. | **Not built** |
| Worker history / cleanup | Completed ephemeral attempts, disk usage, and cleanup actions. | **Not built** — no such view exists under `internal/webui/frontend/src/views`; cleanup is CLI-only (`internal/cli/cleanup/sessions_cmd.go:26`). |

## Agent Sidebar

Each agent row should show:

- agent name
- role
- backend/model
- lifecycle state
- current task, if any
- runtime provider
- stale/offline indicator
- latest error summary, if any

Example:

```text
codex-coder    task / codex    running    FLEETDB-1860    local process
```

The sidebar is primarily a live/reconnectable agent surface. It should show:

- persistent/service agents
- lead agents
- currently running or reconnectable ephemeral workers

It should not show completed ephemeral workers as idle agents. Completed
ephemeral workers belong in worker history and task attempt history.

### Empty State

When no agents exist:

```text
No agents configured.
Create a planner or coder agent to start processing tasks.
```

When agents exist but none are running:

```text
All agents idle.
Ready tasks: 4. Needs planning: 2.
```

## Task Card Indicators

Task cards should show:

- assigned/claiming agent
- active run state
- session count
- blocked/failed badge when latest run failed
- stale badge when the run stopped heartbeating

Examples:

```text
FLEETDB-1859
Planning probe
claimed by codex-designer · session running
```

```text
FLEETDB-1860
Coding probe
completed by codex-coder · 1 file changed · push failed
```

For epic-runner tasks, the card should also show the latest worker attempt:

```text
E2E-2
Reset password flow
closed | worker-e2e-2-a1 completed | logs | diff | cleanup
```

If a task has multiple attempts, the card shows the latest attempt and exposes
the full attempt list in the issue detail panel.

## Issue Detail: Timeline

The issue timeline should merge task events and run events:

```text
18:36:23  codex-designer run started
18:36:42  claimed FLEETDB-1859
18:37:44  design updated
18:37:48  status transition to review failed
18:38:24  run completed
```

Timeline entries should include:

- timestamp
- actor/agent
- event type
- short message
- linked artifact when available

## Sessions Tab

The Sessions tab lists every session attached to the task.

Each session row shows `session_id`, `agent_name`, `backend`/`model`, `phase`,
`status`, `started_at`/`ended_at`, `duration_s`, token usage, and artifact
badges. Every one of those maps to a `sessions.SessionRecord` field
(`internal/sessions/types.go:30-75`); token usage is
`usage.SessionUsage` (`internal/usage/usage.go:14-29`). Do not restate the
field list here — see
[`session-artifact-contract.md`](session-artifact-contract.md).

`status` values depend on which store the row came from:
`running|completed|failed|aborted` for filesystem records, the ten-value
`domain.AgentSessionStatus` for control-plane records. They are different
enums and `stale` is in neither — see
[`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md) and
[`session-stores.md`](session-stores.md).

### Empty State

If the task has no sessions:

```text
No sessions recorded for this task.
Runs started outside the Loom runtime may not publish sessions yet.
```

If the task has an active claim but no session:

```text
Task is claimed, but no session was published.
This usually means the agent was started outside the supervised runtime.
```

For ephemeral workers, the Sessions tab is the task's attempt history. Each
ephemeral worker session is one task attempt. Rows should show:

- attempt number (`attempt_num` / `attempt`)
- worker agent name
- lead attribution via `parent_session_id`
  (`internal/domain/control_plane.go:89`) — there is no `lead_agent_name`
- status (per the enum note above)
- duration
- artifact badges
- cleanup state — the **only** value ever written is `retained`, in the
  session metadata map for ephemeral-mode agents
  (`internal/cli/daemon/supervisor/supervisor.go:632-635`). "archived" and
  "deleted" are proposed, not written by any code path.
- actions: terminal when live; logs/diff when complete

Completed ephemeral attempts must be read-only from this tab. Rerun creates a
new attempt instead of restarting the completed worker.

## Logs and Transcript

Logs and transcript should be distinct:

| Data | Meaning |
|---|---|
| Transcript | AI/backend conversation and tool events. |
| Logs | Process output, supervisor output, runner lifecycle logs. |
| Scrollback | Terminal/PTY output. |

UI controls (`jump to first error` and `show redacted credential notices` are
proposals; the rest are the ordinary viewer controls):

- filter by text
- copy selected lines
- download full log/transcript
- jump to first error *(proposed)*
- show redacted credential notices *(proposed)*

Completed ephemeral workers must keep logs/transcript available after the live
PTY is gone. If scrollback cannot be preserved, process logs and transcript
still need to be enough to explain the run.

## Diffs and Files

When a run changes files, the UI should show:

- changed file count
- lines added/removed
- file list
- diff viewer
- commit ID, if committed
- push result, if attempted

If a run has a commit but no push:

```text
Committed locally. Push not completed.
Reason: no origin remote configured.
```

## Failure States

Failures should be readable without opening raw logs. Render the real class
strings — see [`error-class-reference.md`](error-class-reference.md) — never
invented ones.

Local preflight failure. The message loom actually produces
(`internal/runtimepreflight/preflight.go:98-101`):

```text
local task runner cannot start: backend "codex" is missing auth (<detail>);
set the provider credentials or switch the Project Default Backend
(local_backend_auth_missing)
```

Agent-invocation failure, keyed on the persisted `error_class` verbatim:

```text
Session failed: AuthFailure
Action: re-authenticate the backend CLI.
```

Gate failure has no error class today — a failing gate command surfaces as
ordinary command output. Do not key UI on a `gate_failed` class; it does not
exist.

Stale is not a class either; see the two mechanisms described in
[`failure-modes-recovery-ux.md`](failure-modes-recovery-ux.md#stale-runner-ux).

## Stale and Offline States

Observed runtime state must carry freshness.

The UI should show:

- last heartbeat time
- last observed node/container/process
- stale threshold
- whether the task claim is still valid

Stale data must not look active.

## Actions

Common user actions:

- start planner
- start coder
- stop/yield agent
- retry failed run
- release stale claim
- open logs
- open transcript
- open diff
- delete completed worker worktree *(proposed — CLI only today)*
- archive run artifacts *(proposed — no code path)*
- bulk cleanup completed ephemeral workers *(proposed — CLI only today)*
- mark design approved
- mark blocked/unblocked

Actions should be disabled with clear reasons when unavailable.

## Proposed — not built

Everything in this section describes surfaces that do not exist as of
2026-07-23. Kept because the design intent is still the intent.

### Run Detail Panel

A per-attempt panel showing the full session metadata and artifacts, replacing
the live terminal once an ephemeral worker completes. It should answer: what
task did this attempt run, which lead spawned it, what changed, did it pass
gates, where are logs and transcript, how much disk is still retained, which
cleanup actions are available.

The field list is
[`session-artifact-contract.md`](session-artifact-contract.md). Note two
things the original sketch got wrong: there is no `run_id` (the identity is
`session_id`), and `role` / `runtime_provider` / `command` are not session
fields.

Sketch:

```text
worker-e2e-2-a1
task: E2E-2 Reset password flow
lead: <parent_session_id>
status: completed
artifacts: logs transcript diff tests
```

`retained: worktree 184 MB` is not achievable as written — no disk-usage
estimate is computed anywhere under `internal/` (there is no `DiskUsage`
symbol). Sizing would have to be added first.

### Worker history / cleanup view

A workspace-level view of completed ephemeral attempts. No such view exists
under `internal/webui/frontend/src/views/` (the shipped views are
AgentsPage, AgentEditorGroups, PRsPage, PRReviewWorkspace, KanbanPage,
ListPage, FilesPage, IssueDetailPage, SettingsPage, WorkspacePage,
MonitorPage, ObservabilityPage, TablePage, GraphPage).

### Cleanup Actions

Cleanup actions apply only after evidence has been persisted.

The shipped cleanup surface is the CLI: `loom cleanup` with `sessions`,
`usage`, and `events` subcommands
(`internal/cli/cleanup/cleanup_cmd.go:30`,
`internal/cli/cleanup/sessions_cmd.go:18,26`), backed by
`Store.PurgeOlderThan` (`internal/sessions/purge.go:15`). There is no bulk UI
and no filters.

Proposed per-run cleanup:

```text
View logs
View transcript
View diff
Open worktree
Delete worktree
Archive artifacts
Rerun task
```

Proposed workspace-level cleanup:

```text
filters: lead, epic, task, status, age, disk usage
bulk actions: delete worktrees, archive artifacts
```

Deleting a worktree must not delete the run metadata, session record, logs,
transcript, diff summary, or final status.

## Acceptance Criteria

- A run appears in the UI before model invocation begins.
- A task with an active run shows the agent name on the card.
- A completed run remains visible after process/container exit.
- A completed ephemeral worker appears in lead/task history, not as an idle
  live agent.
- A user can find completed ephemeral worker history from the lead page and
  the task detail panel. *(The workspace cleanup/history view does not
  exist.)*
- A user can delete completed ephemeral worker worktrees while preserving run
  evidence. *(CLI only — `loom cleanup sessions clean`.)*
- Empty states explain missing data and likely cause.
- Stale/offline state is visually distinct from running state.
- A user can find logs, transcript, diff, and test result from the task
  detail panel.

## Related

- [`session-artifact-contract.md`](session-artifact-contract.md) — the data behind every surface here
- [`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md) — status vocabularies
- [`error-class-reference.md`](error-class-reference.md) — failure strings to render
- [`session-stores.md`](session-stores.md) — why a claimed task can show no session
- [`failure-modes-recovery-ux.md`](failure-modes-recovery-ux.md) — recovery behavior
- [`lead-agent-epic-runner-spec.md`](lead-agent-epic-runner-spec.md) — the lead page
