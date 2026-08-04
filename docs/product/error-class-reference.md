# Error Class Reference

> **Status:** Current · *audited 2026-08-03*

**Last updated:** 2026-08-03
**Related:** [`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md),
[`session-stores.md`](session-stores.md),
[`failure-modes-recovery-ux.md`](failure-modes-recovery-ux.md)

## Purpose

`internal/runtimepreflight/preflight.go:92` refers to "the error-class registry
(§4.5)". No such registry existed in `docs/`. This file is it.

Loom has **three** distinct failure vocabularies. They are not
interchangeable, they are not states (see
[`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md)), and
the previous product docs invented a fourth that no code emits. §3 enumerates
every literal loom's own Go code writes to a persisted `error_class` field
today. Anything not listed below is not a loom error class — with one
exception, called out in §3: a task-runner bundle supplies its own `errorClass`
and loom copies it through verbatim.

## 1. `agenterr.Outcome` — the classified failure of one agent invocation

`Outcome` is the value that travels through `AgentError.Class`
(`internal/agenterr/error.go:9`), telemetry, and retry-policy lookup. It is
a union of two halves: a harness-output class owned by the harness wrapper, and
a loom-domain outcome that no subprocess could have produced
(`internal/agenterr/outcome.go:35-47`).

`Outcome.String()` is the wire format. The strings are deliberately
byte-stable because they are serialized into `daemon-agents.json`
`last_error_class`, events, and checkpoints (`internal/agenterr/outcome.go:67-76`).
Checkpoints are only *mostly* `Outcome` names: the yield path writes the
non-`Outcome` sentinel `Yielded` into the same field (§3).

### Harness half — `wrapper.ErrorClass`

From `github.com/olesho/harness-wrapper` (pinned at
`v0.7.6-0.20260718021055-003291869238`, `go.mod:17`), declared at
`pkg/wrapper/errorclass.go:16-29`, names at `:36-56`.

| `String()` | Meaning |
|---|---|
| `None` | Zero value: clean exit / waiting for input / idle. Not an error. |
| `RateLimited` | 429, usage or session limit. Transient; resets. |
| `AuthFailure` | 401, invalid key. Fatal. |
| `BillingError` | 402, payment required, insufficient credits, quota exceeded. Fatal. |
| `ModelNotFound` | 404, model does not exist. |
| `ContextOverflow` | Context length / token limit exceeded. Reserved; not yet emitted by built-in packs. |
| `Timeout` | Request or connection timeout, deadline exceeded. |
| `Transient` | 5xx, transport reset, temporary failure. |
| `Unknown` | Unclassifiable failure. |

Go identifiers differ from the wire names on purpose (`ErrAuth` →
`"AuthFailure"`); use the `String()` column everywhere outside Go.

### Domain half — `agenterr.DomainOutcome`

Failure modes decided by loom's own coordination logic, which the wrapper
cannot represent (`internal/agenterr/outcome.go:12-33`).

| `String()` | Meaning |
|---|---|
| `NoWork` | No claimable task; epic exhausted. |
| `LockConflict` | fleet-db task is locked by another agent. |
| `SpawnFailure` | Supervisor could not exec the agent subprocess. |
| `BackendUnavailable` | Backend CLI binary not on PATH. Set by loom's own marker regex (`internal/agenterr/classify.go:146`) and folded from the wrapper's `StatusBinaryNotFound` (`internal/agenterr/classify.go:201-202`). |

A domain outcome wins over a harness class when both are set
(`internal/agenterr/outcome.go:71-75`).

## 2. `supervisor.StopReason` — why the agent loop stopped

Distinct question from "what failed": a stop can be entirely successful. Values
at `internal/cli/daemon/supervisor/types.go:76-97`.

| Value | Meaning |
|---|---|
| `no_work` | Nothing claimable. |
| `rate_limited` | Backoff after rate limiting. |
| `max_retries` | Restart budget exhausted, agent abandoned. |
| `fatal_error` | Non-retryable failure. |
| `manual_stop` | Operator stopped it. |
| `config_removed` | Agent definition disappeared from config. |
| `shutdown` | Daemon shutting down. |
| `yielded` | Agent yielded its slot. |
| `watchdog` | Watchdog killed it. |
| `backend_unavailable` | Backend CLI not on PATH. |
| `ephemeral_done` | Ephemeral-mode agent exited cleanly after one successful task. |
| `max_retries_blocked` | Budget exhausted, but the policy blocks-and-retries on a fixed interval instead of abandoning; the supervise goroutine stays alive and self-resumes. |
| `fast_fail` | Deterministic failure the policy refuses to retry or block. Surfaced as `failed` in daemon-status. |

## 3. Persisted `error_class` strings

`error_class` is a free-form string on both session records
(`internal/sessions/types.go:74`, `internal/domain/control_plane.go:97`), on the
per-worktree checkpoint file (`internal/cli/config/checkpoint.go:25`, written by
`SaveCheckpoint` at `:32`), and on three platform records —
`domain.DriverRun` (`internal/domain/platform.go:413`), `domain.TaskRun`
(`:535`), and `domain.TriggerDelivery` (`:364`). Four kinds of value reach
them.

**Normal agent exit** writes `ap.LastError.Class.String()` — i.e. an
`agenterr.Outcome` name from §1 — for both the filesystem and control-plane
records (`internal/cli/daemon/supervisor/session_finalize.go:121,168-176`).

**Two ad-hoc snake_case exceptions** bypass `Outcome` entirely:

| Literal | Written at |
|---|---|
| `spawn_failure` | `internal/cli/daemon/supervisor/supervisor.go:797,804` |
| `backend_unavailable` | `internal/cli/daemon/supervisor/session_finalize.go:86,94` |

**The checkpoint file** (`.agent.checkpoint.json`) is a third carrier. The crash
path writes an `Outcome` name (`internal/cli/daemon/supervisor/classify.go:150`,
from `ap.LastError.Class.String()` at `:134`), but the yield path writes the
literal `Yielded` (`classify.go:193`) — which is not an `agenterr.Outcome`, not a
`supervisor.StopReason` (that vocabulary's value is lowercase `yielded`, on a
different field), and not one of the snake_case literals here. So one JSON field
carries two vocabularies; read it alongside `yield_reason`, which the yield path
always sets and the crash path never does.

**Driver and task runs** (`domain.DriverRun` / `domain.TaskRun` /
`domain.TriggerDelivery` — a different subsystem from agent sessions) use their
own set. It is wider than `internal/driver/executor.go`:

| Literal | Record | Written at |
|---|---|---|
| `bundle_verification` | DriverRun | `internal/driver/executor.go:165` |
| `driver_cancelled` | DriverRun | `internal/driver/executor.go:173,876` |
| `driver_runtime` | DriverRun | `internal/driver/executor.go:175,884` |
| `invalid_driver_result` | DriverRun | `internal/driver/executor.go:349,918,926` |
| `sandbox_required` | DriverRun, TaskRun | const `internal/driver/sandbox/policy.go:27`; DriverRun via `policy.go:85` (called from `executor.go:762`), TaskRun at `internal/driver/task_bridge_session.go:139` |
| `superseded` | DriverRun, TriggerDelivery | `internal/infra/memstore/platform_driver_run.go:364`, `internal/infra/memstore/platform_trigger.go:800` |
| `stale_task_run` | TaskRun | const `internal/driver/stale_task_sweeper.go:21`, written at `:82` |
| `local_worktree_unprovisioned` | TaskRun | const `internal/driver/task_worktree_resolver.go:22`, written at `internal/driver/task_bridge_session.go:90` |
| `invalid_task_result` | TaskRun | `internal/driver/task_bridge_session.go:61`, `internal/driver/task_request.go:815` |
| `provider_unsupported` | TaskRun | `internal/driver/task_request.go:156,176` |
| `task_executor_error` | TaskRun | `internal/driver/task_request.go:791` |
| `patch_back_base_required` | TaskRun | `internal/driver/task_bridge.go:847` |

The task-run set is not closed. A runner bundle reports its own `errorClass` in
the task result and loom copies it through — see the sibling patch-back path at
`internal/driver/task_bridge.go:930`, which falls back to the runner's status
when no class was given. The built-in bundles under `internal/workflows/builtin/`
are the main producers; `local-task-runner.ts` alone emits
`local_backend_unsupported` (`:72`), `local_worktree_missing` (`:81`),
`local_backend_unavailable` (`:92`), `local_agent_failed` (`:153`),
`github_credentials_missing`, `github_repo_unresolved`, `stack_branch_missing`,
`stack_push_failed`, and `github_pr_failed` (`:185-221`, via `failed()` at
`:1665`). Treat runner-supplied classes as an open set keyed by bundle, not as
part of loom's vocabulary.

Not persisted, but shaped like one of these: `flue_build_failed`, a field of
`loom workflow build --json` output (`internal/cli/workflow/workflow_cmd.go:250`).

## Preflight identifiers

`PreflightLocalTaskRunner` (`internal/runtimepreflight/preflight.go:77`) gates on
`backends.CheckBackendHealth`, which answers three things: the backend is
registered and implements `HealthCheckableBackend` at all
(`internal/cli/backends/backend_capabilities.go:154-162`), the binary is on
PATH, and auth is present. Two of its four failure returns end in a
parenthesised identifier in the **message text**:

- `local_backend_unavailable` — CLI not installed (`preflight.go:94-97`)
- `local_backend_auth_missing` — auth missing (`preflight.go:98-101`)

The other two carry no identifier: an unknown/unregistered backend, or one with
no health check, returns `... is not available for health checks; set a
supported Project Default Backend ...` (`preflight.go:81-86`), and the residual
unhealthy case returns `... is not ready (%s)` (`preflight.go:102-104`). The
latter is defensive only — every built-in backend computes `Healthy` from
`Installed` and `APIKeySet`, so the `default` arm is reachable only via a
third-party `loom-backend-*` external backend.

*Here* these are substrings of an error message, not an `error_class` field
value, and callers assert on them as such
(`internal/runtimepreflight/preflight_test.go:82,100`,
`internal/cli/epic/run_test.go:75`, `internal/cli/workflow/workflow_cmd_test.go:271`).
A failed preflight prevents the run from being queued, so no session and no
persisted `error_class` is produced. `local_backend_unavailable` is *also* a
real runner-supplied `errorClass` in a different place — the built-in
`local-task-runner.ts` bundle emits it at `:92` when the run did get queued
(§3) — so do not treat the two occurrences as one code path.

## Do not use

The following appear in older drafts of
[`failure-modes-recovery-ux.md`](failure-modes-recovery-ux.md) and
[`session-artifact-contract.md`](session-artifact-contract.md). None is emitted
as an error class by any code path: `preflight_failed`, `auth_failed`,
`tool_missing`, `worktree_missing`, `model_failed`, `command_failed`,
`gate_failed`, `push_failed`, `runner_lost`, `stale`. The first nine have zero
occurrences under `internal/` at all; `stale` occurs, but only as an unrelated
display string and RPC op name (`internal/cli/workspace/ops_cmd.go:754`,
`internal/rpc/protocol.go:21`) — never as an `error_class`.

## Related

- [`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md) — state vocabularies (error classes are not states)
- [`session-stores.md`](session-stores.md) — which record each `error_class` lands on
- [`failure-modes-recovery-ux.md`](failure-modes-recovery-ux.md) — user-facing recovery behavior
- [`agent-messaging-and-backpressure.md`](agent-messaging-and-backpressure.md) — where rate-limit cooldown actually lives
