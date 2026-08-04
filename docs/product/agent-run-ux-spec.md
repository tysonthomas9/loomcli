# Agent Run UX Spec

**Status:** Draft
**Date:** 2026-05-11
**Related:** `docs/product/agent-execution-prd.md`,
`docs/product/agent-lifecycle-state-machine.md`,
`docs/product/session-artifact-contract.md`,
`docs/product/lead-agent-epic-runner-spec.md`

## Purpose

Define what the UI should show for agent execution across local,
daemon-managed, direct CLI, and container/remote runs.

The UI should answer three questions quickly:

- What is running?
- What task does it own?
- What evidence did it leave behind?

## Primary Surfaces

| Surface | Purpose |
|---|---|
| Agent sidebar | Current and recent agent/run status. |
| Kanban card | Task ownership, state, and latest run signal. |
| Issue detail panel | Timeline, sessions, files, logs, and recovery actions. |
| Run detail panel | Full execution metadata and artifacts for one run. |
| Terminal/session view | Live or archived transcript/log stream. |
| Lead agent page | Epic-scoped terminal, tasks, live workers, and worker history. |
| Worker history / cleanup | Completed ephemeral attempts, disk usage, and cleanup actions. |

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

The Sessions tab should list every run attached to the task.

Each session row should show:

- session ID
- agent name
- backend/model
- phase: planning, implementation, review, shell, other
- status
- start/end time
- duration
- token usage
- artifact badges: transcript, logs, diff, tests, commit

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

- attempt number
- worker agent name
- lead/orchestrator session, when present
- status: running, completed, failed, cancelled, stale
- duration
- artifact badges
- cleanup state: worktree present, archived, deleted
- actions: terminal when live; run detail/logs/diff/cleanup when complete

Completed ephemeral attempts must be read-only from this tab. Rerun creates a
new attempt instead of restarting the completed worker.

## Run Detail Panel

The run detail panel should show:

- run ID
- session ID
- task ID
- agent name
- role
- backend/model
- runtime provider
- node/container/process identity
- command
- lifecycle state
- start/end timestamps
- exit code
- error class
- preflight result
- artifacts
- cleanup state
- worktree/container path when server-visible

For completed ephemeral workers, the run detail panel replaces the live
terminal. It should answer:

- what task did this attempt run?
- which lead spawned it?
- what changed?
- did it pass gates?
- where are logs and transcript?
- how much disk is still retained?
- which cleanup actions are available?

Example:

```text
worker-e2e-2-a1
task: E2E-2 Reset password flow
lead: nova
status: completed
artifacts: logs transcript diff tests
retained: worktree 184 MB
actions: open logs | open diff | delete worktree | archive artifacts | rerun
```

## Logs and Transcript

Logs and transcript should be distinct:

| Data | Meaning |
|---|---|
| Transcript | AI/backend conversation and tool events. |
| Logs | Process output, supervisor output, runner lifecycle logs. |
| Scrollback | Terminal/PTY output. |

UI controls:

- filter by text
- copy selected lines
- download full log/transcript
- jump to first error
- show redacted credential notices

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

Failures should be readable without opening raw logs.

Examples:

```text
Preflight failed: Codex auth not found.
Action: run codex login or mount credentials into the runner.
```

```text
Gate failed: no make gate target.
Action: configure the repo gate command or mark this repo as no-gate.
```

```text
Runner stale: no heartbeat for 3m.
Action: inspect logs, restart runner, or release task claim.
```

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
- delete completed worker worktree
- archive run artifacts
- bulk cleanup completed ephemeral workers
- mark design approved
- mark blocked/unblocked

Actions should be disabled with clear reasons when unavailable.

### Cleanup Actions

Cleanup actions apply only after evidence has been persisted.

Per-run cleanup:

```text
View logs
View transcript
View diff
Open worktree
Delete worktree
Archive artifacts
Rerun task
```

Workspace-level cleanup:

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
- A user can find completed ephemeral worker history from the lead page, the
  task detail panel, and the workspace cleanup/history view.
- A user can delete completed ephemeral worker worktrees while preserving run
  evidence.
- Empty states explain missing data and likely cause.
- Stale/offline state is visually distinct from running state.
- A user can find logs, transcript, diff, and test result from the task
  detail panel.
