# Failure Modes and Recovery UX

> **Status:** Partially implemented · *audited 2026-08-03*. The Failure Matrix
> was rewritten against the real vocabularies; the UX mockups below it are
> still proposals and are marked as such.

**Last updated:** 2026-08-03
**Related:** [`error-class-reference.md`](error-class-reference.md),
[`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md),
[`agent-messaging-and-backpressure.md`](agent-messaging-and-backpressure.md),
[`agent-run-ux-spec.md`](agent-run-ux-spec.md),
[`agent-execution-prd.md`](agent-execution-prd.md)

## Purpose

Define user-facing behavior when agent execution fails or produces partial
results.

Failures should become clear product states with recovery actions, not
ambiguous logs.

This document does **not** define the failure vocabulary — it consumes it.
[`error-class-reference.md`](error-class-reference.md) is canonical for error
classes and stop reasons;
[`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md) is
canonical for states.

## Principles

- Fail before model invocation when preflight can detect the issue.
- Preserve evidence for every failure.
- Use the existing vocabularies for filtering and automation. Do not mint new
  class strings in the UI layer.
- Tell the user what happened and what they can do next.
- Avoid leaving tasks stuck in `in_progress` without explanation.

## Failure Matrix

Only classes that a code path actually emits appear here. "Kind" says which
vocabulary the value belongs to: **O** = `agenterr.Outcome`, **S** =
`supervisor.StopReason`, **E** = ad-hoc persisted `error_class` string.
See [`error-class-reference.md`](error-class-reference.md).

| Failure | Value | Kind | Source | Recovery action |
|---|---|---|---|---|
| Backend CLI not on PATH | `BackendUnavailable` / `backend_unavailable` | O / E / S | `internal/agenterr/classify.go:146,202`; persisted `internal/cli/daemon/supervisor/session_finalize.go:86,94`; agent state `internal/domain/agent.go:19` | Install the CLI or switch backend. The reconciler re-checks PATH each tick and returns the agent to `idle` automatically. |
| Provider auth invalid | `AuthFailure` | O | harness-wrapper `pkg/wrapper/errorclass.go:21` (401) | Re-login to the backend CLI or fix the API key. Fatal — not retried. |
| Billing / quota exhausted | `BillingError` | O | `errorclass.go:22` (402) | Fix billing. Fatal — not retried. |
| Model does not exist | `ModelNotFound` | O | `errorclass.go:23` (404) | Correct the model name or fall back to another backend. |
| Context window exceeded | `ContextOverflow` | O | `errorclass.go:24` — reserved, not yet emitted by built-in packs | Reduce the prompt/transcript. Policy treats it as fast-fail. |
| Model rate limited | `RateLimited` | O | `errorclass.go:20` (429) | Automatic: cooldown honours `RetryAfter`, else 60s (`internal/cli/automode/automode_task.go:277-282`). See [`agent-messaging-and-backpressure.md`](agent-messaging-and-backpressure.md). |
| Request timed out | `Timeout` | O | `errorclass.go:25` | Retry. |
| Upstream 5xx / transport reset | `Transient` | O | `errorclass.go:26` | Retry. |
| Unclassifiable failure | `Unknown` | O | `errorclass.go:27` | Open logs and transcript. |
| Supervisor could not exec the agent | `SpawnFailure` / `spawn_failure` | O / E | `internal/agenterr/classify.go:153-157`; persisted `internal/cli/daemon/supervisor/supervisor.go:797,804` | Inspect logs, retry, release claim. |
| Task locked by another agent | `LockConflict` | O | `internal/agenterr/outcome.go:15` | None needed; the other agent owns it. |
| Nothing claimable | `NoWork` / `no_work` | O / S | `internal/agenterr/outcome.go:14`; `internal/cli/daemon/supervisor/types.go:76` | Not a failure. Create or unblock tasks. |
| Restart budget exhausted | `max_retries` / `max_retries_blocked` | S | `types.go:78,92` | `max_retries_blocked` self-resumes on a fixed interval; `max_retries` abandons the agent. |
| Policy refuses to retry | `fast_fail` | S | `types.go:97` | Surfaced as `failed` in daemon-status. Fix the root cause and restart. |
| Watchdog killed the agent | `watchdog` | S | `types.go:84` | Inspect logs, restart. |
| Local preflight: CLI missing | `local_backend_unavailable` | message text | `internal/runtimepreflight/preflight.go:94-97` | Install the CLI or change the Project Default Backend. No session is created. |
| Local preflight: auth missing | `local_backend_auth_missing` | message text | `internal/runtimepreflight/preflight.go:98-101` | Set provider credentials or change the Project Default Backend. No session is created. |

Failures the earlier draft listed that **no code path produces** — missing
`jq`, missing gate command, gate failure, missing git remote, rejected status
transition — are not classified failures today. They surface as ordinary
command output. Do not build UI keyed on class names for them until a class
exists.

## Preflight Failure UX

`PreflightLocalTaskRunner` (`internal/runtimepreflight/preflight.go:77`) is
fail-closed: on failure the run is never queued, so no session and no
persisted `error_class` exists to render. It gates on
`backends.CheckBackendHealth`, which answers three things — the backend is
registered and health-checkable at all, the binary is on PATH, and auth is
present. The six other checks the PRD proposed (workspace path binding,
worktree, required tools, gate command, git remote, session publication) do not
exist.

Only two of the four failure returns carry a parenthesised identifier (the two
matrix rows above). An unknown/unregistered backend (`preflight.go:81-86`) and
the residual not-ready case (`preflight.go:102-104`) return a plain message with
no identifier, so UI must not key preflight handling on the identifier alone.

The real failure text is the returned error, e.g.:

```text
local task runner cannot start: backend "codex" is missing auth (<detail>);
set the provider credentials or switch the Project Default Backend
(local_backend_auth_missing)
```

> **Proposed, not built.** A dedicated preflight panel with "Open setup
> guide / Retry preflight / Change backend" actions, and a visible task note
> when a task was already selected.

## Runtime Failure UX

Runtime failures should show:

- error class
- exit code
- failing command
- short output excerpt
- full log link
- retry action

Layout sketch. The class string must come from the persisted `error_class`
verbatim; the reason line is whatever the backend produced.

```text
Session failed: AuthFailure
Exit code: <exit_code>
Reason: <summary>
```

## Stale Runner UX

Two unrelated mechanisms are both loosely called "stale". Neither is an error
class named `stale`.

**KV stale detector.** Runs only when a Redis address is configured. Without
one, `InitStaleDetectorHandler` returns a handler that reports the detector
disabled (`internal/cli/serve/daemonwire/stale.go:17-20`). Its KV client is
wrapped in a `circuitbreaker` with a 5-failure threshold and a 30s open
timeout (`stale.go:24-29`).

**Filesystem session self-heal.** Independent of Redis. A session still
`running` with no `ended_at` after `StaleSessionThreshold = 4h` is rewritten
to `aborted`, with `ended_at = started_at + 4h`, and persisted
(`internal/sessions/stale.go:12,24-38`). This heal is **one-way and
destructive**: a runner that resumes after the rewrite does not get its
`running` status back. An earlier draft of this document claimed stale was
reversible; it is not, for filesystem sessions.

> **Proposed, not built.** A stale-runner panel offering "Inspect logs / Mark
> run failed / Release task claim / Retry in new runner". Note that any such
> panel must distinguish the two mechanisms above, because only one of them is
> even running in a default local install.

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

> **Proposed, not built.** The shipped recovery surface is CLI-side:
> `loom recover <worktree>` (`internal/cli/agent/recover.go:26`),
> `loom doctor` (`internal/cli/doctor/doctor.go:71`), and
> `loom cleanup sessions clean` (`internal/cli/cleanup/sessions_cmd.go:26`).

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

- Every failed session has an error class drawn from
  [`error-class-reference.md`](error-class-reference.md) and a recovery action.
- Preflight failures do not invoke the model. *(Met —
  `internal/runtimepreflight/preflight.go:69-77` is fail-closed.)*
- Stale runners are visible and recoverable.
- Partial success preserves artifacts.
- Tasks do not remain `in_progress` silently after terminal session failure.

## Related

- [`error-class-reference.md`](error-class-reference.md) — canonical failure vocabularies
- [`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md) — canonical state vocabularies
- [`agent-messaging-and-backpressure.md`](agent-messaging-and-backpressure.md) — where rate-limit cooldown lives
- [`agent-run-ux-spec.md`](agent-run-ux-spec.md) — the surfaces these failures render in
- [`session-artifact-contract.md`](session-artifact-contract.md) — evidence preserved on failure
