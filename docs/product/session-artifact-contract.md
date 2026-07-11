# Session and Artifact Contract

**Status:** Draft
**Date:** 2026-05-11
**Related:** `docs/product/agent-execution-prd.md`,
`docs/product/agent-run-ux-spec.md`,
`docs/product/container-runner-mvp-spec.md`,
`docs/product/lead-agent-epic-runner-spec.md`

## Purpose

Define the product-level contract for what every agent run must produce.

This contract applies to:

- local daemon agents
- direct CLI runs
- UI-started local runs
- container/remote workers

## Contract Summary

Every agent run should produce:

- run metadata
- session metadata
- task association
- transcript or log evidence
- final status
- error class when failed
- artifacts for changed files, tests, commits, and push attempts when
  applicable

## Required Metadata

| Field | Required | Notes |
|---|---|---|
| run_id | yes | Stable ID for one execution attempt. |
| session_id | yes | Stable ID for transcript/log grouping. |
| workspace_id | yes | Workspace scope. |
| agent_name | yes | Actual claimant/executor identity. |
| lead_agent_name | no | Required when the run was spawned by a lead agent. |
| parent_session_id | no | Required when the run was spawned by another agent session. |
| role | yes | Planner, task, reviewer, or custom role. |
| backend | yes | Codex, Claude, OpenCode, shell, etc. |
| model | no | Required when backend reports it. |
| runtime_provider | yes | local, daemon, podman, remote, etc. |
| task_id | no at create, yes after claim | Filled when task is selected. |
| attempt_kind | no | `ephemeral_task_attempt` for single-use epic workers. |
| status | yes | Lifecycle state. |
| started_at | yes | Server or runner timestamp. |
| ended_at | no | Required for terminal states. |
| exit_code | no | Required when process exits. |
| error_class | no | Required for failure terminal states. |

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

When available:

- input tokens
- output tokens
- cache read tokens
- cache write tokens
- estimated cost
- model/pricing tier

Missing usage should be represented as unknown, not zero, when the backend
does not report it.

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

- `attempt_kind = ephemeral_task_attempt`
- `agent_name` = worker agent name
- `task_id` = assigned task
- `lead_agent_name` or `parent_session_id`, when spawned by a lead
- `workspace_id`
- `runtime_provider`
- `status`
- `started_at`
- `ended_at` for terminal states
- `exit_code` or `error_class` for terminal states

Ephemeral task attempts must not be reused for another task. A rerun creates a
new session and a new worker attempt identity.

## Cleanup Metadata

Runs that retain local disk state must report cleanup metadata:

- worktree/container path when server-visible
- disk usage estimate
- retained artifact kinds
- cleanup state: retained, archived, worktree_deleted, artifacts_deleted
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

Failures should use stable classes:

- preflight_failed
- auth_failed
- tool_missing
- worktree_missing
- no_work
- model_failed
- rate_limited
- command_failed
- gate_failed
- push_failed
- runner_lost
- stale
- unknown

## Required Terminal States

A run must finish in one of:

- completed
- failed
- blocked
- aborted
- stale

Terminal state must include an explanation suitable for UI display.

## Storage Requirements

- Artifacts must survive process/container exit.
- UI APIs must be able to list artifacts by task and run.
- Filesystem paths may be stored as local artifact references only when
  the server can read them.
- Distributed/container artifacts should use server-visible storage.
- Deleting an ephemeral worker worktree must not delete run/session metadata or
  artifact references required for history views.

## FleetDB Control-Plane Requirements

Agent sessions are the canonical run visibility record for local mode and
distributed mode. Runners must create and update them through FleetDB, then
attach artifact references to the same `session_id` and `task_id`.

Minimum FleetDB session fields:

- `workspace_key`
- `session_id`
- `agent_id`
- `node_id`
- `kind`
- `phase`
- `status`
- `task_id`
- `started_at`
- `last_heartbeat`
- `finished_at`
- `exit_code`
- `error_class`

The UI must be able to query:

```text
GET /api/v1/{workspace}/agent-sessions?task_id={task_id}
GET /api/v1/{workspace}/agent-sessions?agent_id={agent_id}
GET /api/v1/{workspace}/agent-sessions?parent_session_id={session_id}
```

and render the task Sessions tab, selected lead worker history, and workspace
cleanup/history view without reconstructing ownership from local filesystem
paths.

PATCH payloads must use public snake_case JSON field names and omit fields
that are not being changed. Uppercase Go struct fields or null values for
unset fields are invalid product behavior because they break parity between
local and distributed readers.

## Acceptance Criteria

- Every run has metadata, status, and session ID.
- Every failed run has an error class and user-readable message.
- Every ephemeral worker attempt has exactly one task association.
- Completed ephemeral worker attempts remain queryable after their worktree is
  deleted.
- Cleanup metadata tells the UI whether disk is still retained and which
  cleanup actions are available.
- Every file-changing run has diff metadata.
- Every test/gate attempt has a recorded command and result.
- Completed artifacts remain available after runner exit.
