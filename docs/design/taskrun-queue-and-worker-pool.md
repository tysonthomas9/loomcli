# TaskRun Queue And Worker Pool

> **Status:** Current · *audited 2026-07-23*
> **Date:** 2026-07-23 (documents work landed 2026-06-11, commit `491222e25`)

This note documents the execution topology that replaced synchronous child-task
execution. It was written after the fact: the queue landed with no design doc,
and every FleetDB Agent Platform V2 document still describes the pre-queue
synchronous model.

## The Inversion

Before commit `491222e25` ("driver: task queue with worker claim loops and
retry-then-park"), a workflow's `loom.taskRuns.request({ taskId })` was
synchronous — it shelled out to `loom driver exec-task`, ran the agent, applied
patch-back, and returned a `TaskRunResult`.

It no longer executes anything. `request()` **enqueues**:

- `sdk/driver.js:308` posts the `exec-task` driver op with
  `{ ...params, enqueueOnly: true }`.
- `internal/webui/handlers/driverapi/module.go:530` reads that flag
  (`EnqueueOnly bool`), and `:592` takes the enqueue branch.
- The result is a durable `TaskRun` in status `queued`
  (`internal/domain/platform.go:482`) with no node, no lease, and no fencing
  token yet (`internal/driver/task_request_test.go:265` asserts exactly that).

Execution is done elsewhere, by a pool the workflow never sees.

## The Worker Pool

`loom serve` starts a pool of TaskRun claim loops:

- `internal/cli/serve/serve.go:319` builds a `driverexecutor.TaskWorker`
  template (store, workspace key, work dir, node ID, runner ID, max attempts,
  API base URL).
- `internal/cli/serve/serve.go:349` calls `startDriverTaskWorkers`.
- `internal/cli/serve/serve.go:390-402` runs one goroutine per concurrency slot,
  each with a distinct runner identity (`loom-serve-task-worker-<n>`), ticking
  every second and calling `TaskWorker.RunOnce`
  (`internal/driver/task_worker.go:48`).
- Concurrency is `LOOM_DRIVER_TASK_WORKER_CONCURRENCY`, default 2, clamped to 32
  (`internal/cli/serve/serve.go:462-464`, documented in serve's help at
  `internal/cli/serve/serve.go:127`).

Each `RunOnce` claims one queued `TaskRun` through the fenced store contract,
heartbeats it, executes it in the resolved worktree, and finishes it. The worker
back-links the parent `DriverStep` after the claim
(`internal/driver/task_worker.go:133`).

The pool lives inside the driver-executor startup path, so it shares the
`LOOM_DRIVER_EXECUTOR` toggle (default on;
`internal/cli/serve/serve.go:453-459`). The stale-TaskRun sweeper does **not** —
it is unconditional server policy
(`internal/cli/serve/serve_loops.go:25-30`).

## Why A Workflow Must Not Block On A Task

Because `request()` returns immediately, a workflow needs a way to learn that a
child task finished. There are two, and they are not equivalent:

| Surface | Mechanism | Status |
|---|---|---|
| `loom.taskRuns.await(...)` | client-side polling loop | kept for compatibility (`sdk/driver.d.ts:425`, `:462`) |
| `loom.epics.watch(...)` | server-sent journal stream | preferred push path |

The shipped builtin epic-runner uses the watch stream, not polling. Its own
header states the contract: "The workflow is edge-triggered: it claims ready
tasks up to maxConcurrency, enqueues each as a TaskRun (the serve task workers
execute and close them), and consumes the epic watch stream for terminal TaskRun
events to top the pipeline back up"
(`internal/workflows/builtin/epic-runner.ts:41-52`).

The watch stream is `GET /api/workspaces/{ws}/driver/watch/epic`
(`internal/webui/handlers/driverapi/module.go:192`); see
[`driver-op-http-api.md`](driver-op-http-api.md) for its handshake and resume
contract.

## Retry, Then Park

Failed attempts are retried by the worker, not by the workflow:

- Attempt count lives in `TaskRun.RuntimeMetadata["scheduler_attempt"]`
  (`internal/driver/task_retry.go:41-49`).
- A failed run with `attempt < MaxAttempts` is requeued through
  `TaskRuns().Requeue` with scheduler metadata
  (`internal/driver/task_retry.go:31`, `:54`) and an exponential backoff of
  `min(30s, 1s<<attempt)` (`internal/driver/task_retry.go:90-99`).
- `MaxAttempts` comes from `LOOM_DRIVER_TASK_RUN_MAX_ATTEMPTS`, default 2,
  clamped to 10 (`internal/cli/serve/serve.go:466-468`).
- When the budget is exhausted, the finish carries `BlockTask: true`
  (`internal/driver/task_request.go:954`), which marks the underlying task issue
  blocked server-side. The store documents the semantics: fenced by the same
  lease/fencing checks as the finish, idempotent, best-effort, and it releases
  the issue claim so re-opening the issue makes it ready again
  (`internal/store/platform_store.go:685-692`).

"Park" is therefore a blocked FleetDB issue plus a failed terminal `TaskRun`, not
an in-memory retry queue. Nothing is lost if `loom serve` restarts.

## What This Invalidates

Three documents describe the pre-queue model and are wrong in the same way:

- [`fleetdb-agent-platform-v2-phased-delivery.md`](fleetdb-agent-platform-v2-phased-delivery.md)
  Phase 2 and Phase 3 deliverables (corrected in place, 2026-07-23).
- [`fleetdb-agent-platform-v2-execution-topology-addendum.md`](fleetdb-agent-platform-v2-execution-topology-addendum.md)
  `loom driver exec-task` section (corrected in place, 2026-07-23).
- [`fleetdb-agent-platform-v2-proposal.md`](fleetdb-agent-platform-v2-proposal.md)
  driver-authoring examples.

## Related

- [`workflow-driver-authoring-guide.md`](workflow-driver-authoring-guide.md) —
  the current platform contract for how a workflow runs.
- [`driver-op-http-api.md`](driver-op-http-api.md) — the transport
  `request()`/`watch` ride on.
- [`fleetdb-agent-platform-v2-execution-topology-addendum.md`](fleetdb-agent-platform-v2-execution-topology-addendum.md)
  — runner vs sandbox placement, stale recovery.
