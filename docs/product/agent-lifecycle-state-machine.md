# Agent Lifecycle State Machine

**Status:** Draft
**Date:** 2026-05-04
**Related:** `docs/product/agent-execution-prd.md`,
`docs/product/agent-run-ux-spec.md`,
`docs/product/session-artifact-contract.md`

## Purpose

Define canonical states for agent definitions and agent runs.

The product needs separate but related state machines:

- agent definition state: long-lived configured agent
- run state: one execution attempt
- task state: issue/task lifecycle

## Agent Definition States

| State | Meaning |
|---|---|
| registered | Agent definition exists but is not currently running. |
| enabled | Agent is allowed to start automatically. |
| disabled | Agent exists but should not start. |
| starting | A runtime is being created for the agent. |
| idle | Agent is online but has no task. |
| running | Agent is online and executing a run. |
| stopping | Stop/yield requested. |
| stopped | Agent is intentionally stopped. |
| stale | Last observed heartbeat is too old. |
| error | Agent could not start or reconcile. |

## Run States

| State | Meaning |
|---|---|
| queued | Run requested but not started. |
| starting | Runtime/process/container is starting. |
| preflight | Preconditions are being checked. |
| running | Backend invocation is active. |
| claimed_task | Run has claimed a task. |
| finalizing | Backend exited; artifacts/status are being saved. |
| completed | Run finished successfully. |
| failed | Run failed. |
| blocked | Run found a tracked blocker. |
| aborted | Run was intentionally stopped before completion. |
| stale | Runner disappeared or stopped heartbeating. |

## Task States Relevant to Agents

| State | Meaning |
|---|---|
| open | Available for planning or implementation. |
| in_progress | Claimed by an agent/user. |
| review | Design or implementation awaits human review. |
| blocked | Cannot proceed until blocker is resolved. |
| closed | Completed. |

## Expected Transitions

### Run

```text
queued -> starting -> preflight -> running
running -> claimed_task
claimed_task -> finalizing
finalizing -> completed
finalizing -> failed
finalizing -> blocked
running -> failed
running -> aborted
running -> stale
stale -> failed
```

### Agent Definition

```text
registered -> starting -> idle
idle -> running
running -> idle
running -> stale
stale -> idle
idle -> stopping -> stopped
stopped -> starting
registered -> disabled
disabled -> registered
```

### Task

```text
open -> in_progress
in_progress -> review
in_progress -> blocked
in_progress -> closed
review -> open
blocked -> open
```

## UI Mapping

| Run state | UI tone | User action |
|---|---|---|
| queued | neutral | cancel |
| starting | active | view logs |
| preflight | active | view checks |
| running | active | stop/yield |
| claimed_task | active | open task/session |
| finalizing | active | view artifacts |
| completed | success | open artifacts |
| failed | error | retry/view logs |
| blocked | warning | open blocker/retry |
| aborted | neutral | retry |
| stale | warning/error | inspect/release claim |

## State Invariants

- A run can have at most one active task claim.
- A task should not have multiple valid active claims.
- A terminal run state must include `ended_at`.
- Failed, blocked, aborted, and stale runs must include a reason.
- Stale is observed state; it should not silently mutate task state without
  an explicit recovery policy.

## Acceptance Criteria

- UI and API use the same state vocabulary.
- Invalid transitions return clear validation errors.
- Status transitions used by planner/coder workflows work in FleetDB.
- Stale state is displayed differently from running state.
- Task state and run state are not conflated.
