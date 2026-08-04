# Failure Modes and Recovery UX

**Status:** Draft
**Date:** 2026-05-04
**Related:** `docs/product/agent-execution-prd.md`,
`docs/product/agent-run-ux-spec.md`,
`docs/product/agent-lifecycle-state-machine.md`

## Purpose

Define user-facing behavior when agent execution fails or produces partial
results.

Failures should become clear product states with recovery actions, not
ambiguous logs.

## Principles

- Fail before model invocation when preflight can detect the issue.
- Preserve evidence for every failure.
- Use stable error classes for filtering and automation.
- Tell the user what happened and what they can do next.
- Avoid leaving tasks stuck in `in_progress` without explanation.

## Failure Matrix

| Failure | Error class | User-facing message | Recovery action |
|---|---|---|---|
| Backend CLI missing | tool_missing | Codex/Claude is not installed. | Install backend or choose another backend. |
| Auth missing | auth_failed | Backend credentials are unavailable. | Login or mount credentials. |
| Worktree missing | worktree_missing | Agent worktree does not exist. | Repair/create worktree. |
| Workspace path missing | preflight_failed | Workspace path is unavailable on this node. | Bind/create workspace path. |
| `jq` missing | tool_missing | Required helper tool `jq` is missing. | Install tool or use fallback mode. |
| Gate command missing | preflight_failed | No configured gate command exists. | Configure gate or mark repo no-gate. |
| Gate fails | gate_failed | Quality gate failed. | Open test output and retry. |
| Git remote missing | push_failed | Cannot push because no remote exists. | Add remote or mark push not required. |
| Model rate limited | rate_limited | Backend rate limit reached. | Retry after cooldown. |
| Runner killed | runner_lost | Runner stopped unexpectedly. | Inspect logs, retry, release claim. |
| Heartbeat stale | stale | Runner has not heartbeated recently. | Wait, inspect, or mark failed. |
| Status transition rejected | command_failed | Task status update was rejected. | Show allowed transitions and retry. |

## Preflight Failure UX

Preflight failures should appear before the backend starts:

```text
Preflight failed
Codex credentials were not found in this runner.

Actions:
- Open setup guide
- Retry preflight
- Change backend
```

If a task was already selected, add a visible task note and keep the task
claim policy explicit.

## Runtime Failure UX

Runtime failures should show:

- error class
- exit code
- failing command
- short output excerpt
- full log link
- retry action

Example:

```text
Run failed: gate_failed
Command: make gate
Exit code: 2
Reason: No rule to make target 'gate'.
```

## Stale Runner UX

When a runner stops heartbeating:

```text
Runner stale
Last heartbeat: 3m ago
Container: loomcli-fullrun-coder

Actions:
- Inspect logs
- Mark run failed
- Release task claim
- Retry in new runner
```

Stale should be reversible if the runner resumes heartbeating before the
timeout policy marks it failed.

## Partial Success UX

Some runs produce useful work but fail a final step.

Examples:

- file changed, gate unavailable
- commit created, push failed
- design written, status transition failed

The UI should show partial artifacts and the failed step separately:

```text
Implementation completed with warning
File changed and committed locally.
Push failed: no origin remote configured.
```

## Recovery Actions

Common actions:

- retry run
- retry preflight
- change backend
- repair worktree
- release claim
- mark blocked
- mark failed
- open logs
- open transcript
- configure gate command
- configure git remote

Actions should be permission-aware and disabled when unsafe.

## Acceptance Criteria

- Every failed run has an error class and recovery action.
- Preflight failures do not invoke the model.
- Stale runners are visible and recoverable.
- Partial success preserves artifacts.
- Tasks do not remain `in_progress` silently after terminal run failure.
