# Workflow Driver Authoring Guide

> **Status:** Current · *audited 2026-07-24*
> **Audience:** developers and agents writing TypeScript workflows for Loom
>
> Every symbol, route, env var, and SDK method named below was re-verified
> against `sdk/driver.d.ts`, `internal/driver`, `internal/workflows`, and
> `internal/webui/handlers/workflows` on 2026-07-24. The code samples were
> corrected in that audit — earlier revisions of this guide showed a bare
> `export async function run(ctx)` module and passed `providerProfile` /
> `supportedProviders` to `taskRuns.request`; neither is valid today. See
> "Module Shape" and "Requesting A Child TaskRun".

This guide describes how a registered TypeScript workflow runs through Loom,
fleet-db, and Flue. The key distinction is:

- `DriverRun` is platform-owned lifecycle and durability.
- The `.ts` workflow is user-owned policy and orchestration.

The platform should make the run reliable. The TypeScript should decide what
work means.

## Core Terms

| Term | Meaning |
|---|---|
| `Driver` | A named TypeScript program registered with Loom/fleet-db (`internal/domain/platform.go:64`). |
| `DriverVersion` | One immutable built artifact for a driver. Runs are pinned to versions (`internal/domain/platform.go:87`). |
| `DriverRun` | One execution request for a driver version, created from an API request, CLI command, trigger, or schedule (`internal/domain/platform.go:396`). |
| `DriverStep` | One recorded step within a run; carries `task_run_id` and `action_ledger_id` (`internal/domain/platform.go:462-469`). |
| `TaskRun` | One auditable execution attempt for a single fleet-db task (`internal/domain/platform.go:498`). |
| `ActionLedger` | Idempotent record of an external side effect. **fleet-db-side** — there is no `domain.ActionLedger` in this repo; it surfaces here only as `DriverStep.ActionLedgerID` (`internal/domain/platform.go:469`) and as the `close_task` flag on the task-run completion body (`internal/infra/fleetdb/platform.go:743`). |

## How A Workflow Starts

The simple HTTP path is:

```text
POST /api/workspaces/{ws}/workflows/{name}
  -> create queued DriverRun
  -> driver executor claims it
  -> executor launches the pinned Flue bundle (payload in
     LOOM_FLUE_INVOKE_PAYLOAD)
  -> Flue invokes the module's default-exported defineWorkflow
  -> workflow explicitly returns completed | failed | needs_review
  -> executor finishes the DriverRun
```

`POST /api/workspaces/{ws}/workflows/{name}`
(`internal/webui/handlers/workflows/module.go:40`) does not directly execute the
TypeScript in the request handler. It records durable work and returns
`202 Accepted` with the `DriverRun` (`module.go:174`). A driver executor then
picks up queued runs. `loom serve` enables the DriverRun executor by default;
set `LOOM_DRIVER_EXECUTOR` to `0`, `false`, `off`, or `no` to leave runs queued
(`internal/cli/serve/serve.go:47,455-458`).

If no executor is running, the API still returns `202 Accepted`, but the run
stays `queued`.

Run creation is idempotent on the `Idempotency-Key` request header
(`module.go:165`).

One pre-creation gate is worth knowing about, and it is narrower than it looks:
**only for the builtin `epic-runner` workflow**, and only when the payload's
`runner` is empty or `local-task-runner`, serve preflights that the resolved
agent backend CLI is installed and authenticated, and rejects with `400` before
any `DriverRun` exists (`module.go:154-160`,
`internal/webui/handlers/workflows/preflight.go:23-38`). Every other workflow
name returns early with no check (`preflight.go:24-26`), so a user-registered
workflow gets no such gate — a missing CLI surfaces later, as a failed child
`TaskRun`. "Agent backend" here means the AI CLI (`claude`, `codex`, …) — see
[`../loom-glossary.md`](../loom-glossary.md).

## Registration And Versioning

Before a workflow can run, it must have an active passed `DriverVersion`.

The API registration surface is:

```text
POST /api/workspaces/{ws}/workflows/{name}/versions
```

(`internal/webui/handlers/workflows/module.go:39`.)

Request shape (`createWorkflowVersionRequest`, `module.go:46-50`):

```json
{
  "entrypoint": "workflows/my-workflow.ts",
  "activate": true,
  "files": {
    "workflows/my-workflow.ts": "…see Module Shape below…"
  }
}
```

`entrypoint` is optional; it defaults to `workflows/<name>.ts` (`module.go:74`).
`files` is required (`module.go:69`). The server validates the file set
(`workflows.ValidateWorkflowFiles`), builds it with Flue, registers the
resulting bundle as a `DriverVersion`, and activates it when requested. A later
run uses the active version at run creation time. Editing source after
registration does not change an already registered version.

Two workflows are built in — `epic-runner` and `github-review-agent`
(`internal/workflows/workflows.go:85-88`). They are registered lazily on first
invocation via `workflows.EnsureBuiltinWorkflow`
(`internal/workflows/workflows.go:140`, called from
`resolveWorkflowDriverID`, `internal/webui/handlers/workflows/module.go:179`)
if no user-registered workflow with that name exists.

## Module Shape

A workflow module must **default-export a `defineWorkflow({...})` from
`@flue/runtime`**. Flue no longer normalizes a bare `export function run`
(`internal/workflows/builtin/epic-runner.ts:4-16` documents the change;
`epic-runner.ts:17-20` and `internal/driver/await_flue_e2e_test.go:43-48` are
the shipped shape).

The builtin runners keep a named `run(ctx)` and adapt it from the default
export. The payload arrives through the environment, not Flue's input channel:
the driver launcher sets `LOOM_FLUE_INVOKE_PAYLOAD`
(`internal/driver/executor.go:829`, `internal/driver/sandbox/launcher.go:261`)
and the task-run host bridge sets `LOOM_TASK_RUN_REQUEST_JSON`
(`internal/driver/task_bridge.go:663`; the sibling `FLUE_INTERNAL_CLI_IPC=1`
that gates one-shot IPC mode is set at `task_bridge.go:596`).

```ts
import { createLoomDriverClient } from '@loom/sdk/driver';
import { defineAgent, defineWorkflow } from '@flue/runtime';

export default defineWorkflow({
  // Not an LLM agent: a credential-free stub with no model.
  agent: defineAgent(() => ({ model: false })),
  run: async () => {
    const raw = process.env.LOOM_FLUE_INVOKE_PAYLOAD
      || process.env.LOOM_TASK_RUN_REQUEST_JSON
      || '{}';
    let payload = {};
    try { payload = JSON.parse(raw); } catch { payload = {}; }
    const result = await run({ payload });
    // Flue HEAD rejects undefined in the returned value; round-trip through
    // JSON so optional fields left undefined do not throw.
    return result === undefined ? null : JSON.parse(JSON.stringify(result));
  },
});

export async function run(ctx) {
  const input = ctx.payload || {};
  const loom = createLoomDriverClient({ input });
  return loom.completed({ summary: 'done' });
}
```

The `run(ctx)` samples in the rest of this guide are the *inner* function — wrap
them in the `defineWorkflow` block above.

`createLoomDriverClient` is exported from `@loom/sdk/driver`
(`sdk/driver.d.ts:480`, `sdk/package.json` `exports["./driver"]`). The package
name is pinned Go-side as `driver.LoomDriverSDKPackage`
(`internal/driver/register.go:24`). `createLoomClient` is an alias of the same
function (`sdk/driver.d.ts:481`).

## Platform-Owned Implicit Logic

The platform owns these behaviors. Workflow authors should rely on them instead
of reimplementing them in TypeScript.

| Area | Platform behavior |
|---|---|
| Admission | Creates a `DriverRun` with `queued` status and stores the raw JSON payload. |
| Version pinning | Resolves the active passed `DriverVersion` and stores its ID on the run. |
| Idempotency | Replays run creation when the same idempotency key is used. |
| Claiming | The executor atomically claims a queued run with `node_id`, `lease_id`, and `fencing_token`. |
| Fencing | Mutating driver helper commands verify the run is still owned by the current executor. |
| Heartbeats | The executor heartbeats the claimed `DriverRun` while the workflow runs. |
| Stale recovery | Heartbeat-expired runs can be failed by stale-run recovery. |
| Bundle safety | The executor loads the pinned bundle, verifies manifest refs, and checks the bundle digest. |
| Invocation | The executor starts the built Flue Node server and passes the run payload as `LOOM_FLUE_INVOKE_PAYLOAD` (`internal/driver/executor.go:829`). |
| Runtime errors | Build, bundle, process, and invocation errors become failed `DriverRun`s. |
| Result validation | Missing, malformed, or non-terminal workflow results become failed `DriverRun`s with `invalid_driver_result`. |
| Cancellation | Context cancellation maps to `cancelled`. |
| Finalization | The executor persists status, summary, error class, output metadata, and log refs. |
| Events | Run lifecycle events are available through run event APIs. |

The run status lifecycle is
(`domain.DriverRunStatus`, `internal/domain/platform.go:369-394`):

```text
queued -> running -> completed | failed | needs_review | cancelled
              ^                |
              |                v
              +--- suspended_awaiting_event
```

`needs_review` means the workflow intentionally stopped and returned control to
an operator, reviewer, or lead agent. It is not limited to human review.

`suspended_awaiting_event` is **not terminal** (`platform.go:378-381,388-390`).
A run enters it when the workflow calls `loom.events.await(...)` or
`loom.workflows.await(...)` and no matching event has arrived; the run resumes
when the await resolves or its deadline fires. The SDK surfaces the suspension
as a thrown `WorkflowSuspended` sentinel, not as a result status — the terminal
`LoomDriverResultStatus` union is frozen at
`completed | failed | needs_review | cancelled` (`sdk/driver.d.ts:7`).

## Workflow-Owned Explicit Logic

The `.ts` file owns workflow policy. If behavior should vary by workflow, put it
in TypeScript.

| Decision | Owned by `.ts` |
|---|---|
| Payload interpretation | Read `ctx.payload` and choose input semantics. |
| Task selection | Decide whether to claim one task, all ready tasks, or none. |
| Ordering | Decide sequence, dependency strategy, prioritization, or batching. |
| Concurrency | Decide whether to run child task attempts serially or in parallel. |
| Runner choice | Choose the `runner` (runtime strategy), `workerProfileId`, `nodeId`, and `sandboxPlacement` on each `taskRuns.request`. |
| Success policy | Decide what child `TaskRun` result is acceptable. |
| Task completion | Call `loom.tasks.complete(...)` only when the task should close/unblock. |
| Release policy | Call `loom.tasks.release(...)` when the workflow gives up a claimed task. |
| Review handoff | Return `loom.needsReview(...)` with useful task/log/artifact context. |
| Final result | Return `loom.completed(...)`, `loom.failed(...)`, or `loom.needsReview(...)`. |

The platform does not infer that an epic is drained, that a failed task should
release its claim, or that a successful child task should close a fleet-db task.
Those are workflow decisions.

## Returning A Result

The final value returned from `run(ctx)` is the `DriverRun` result. Prefer the
SDK helpers so status strings stay consistent — their signatures are
`sdk/driver.d.ts:442-450`:

```ts
return loom.completed({ summary: 'all work completed' });
return loom.failed({ summary: 'invalid payload', errorClass: 'invalid_payload' });
return loom.needsReview({
  summary: 'task failed and needs review',
  taskRunId: result.id,
  logsRef: result.logsRef || '',
  artifactsRef: result.artifactsRef || '',
});
```

Every successful code path must return an explicit terminal result. Falling off
the end of `run(ctx)`, returning `{ summary: 'done' }`, returning `{}`, or
returning a non-terminal status such as `running` fails the `DriverRun` with
`error_class = "invalid_driver_result"`.

## fleet-db Task Execution From A Workflow

The driver SDK calls back into Loom's driver API
(`internal/webui/handlers/driverapi/module.go:141-158` registers the op table).
The client resolves the run's credentials from the environment, so TypeScript
never passes leases manually.

| SDK call | Driver-API op | Platform side effect |
|---|---|---|
| `loom.tasks.claimReady({ epicId })` | `claim-ready` | Claims one ready fleet-db task for the current driver run. |
| `loom.taskRuns.request({...})` | `exec-task` | **Enqueues** a child `TaskRun` and returns it — see below. |
| `loom.taskRuns.get({ taskRunId })` | `task-run-get` | Reads the child `TaskRun`. |
| `loom.taskRuns.await({ taskRunId })` | `task-run-get` (polled) | Client-side poll until terminal; `pollMs` default 2000, `timeoutMs` default 0 = no timeout (`sdk/driver.js:321-340`). |
| `loom.epics.watch({ epicId })` | epic watch SSE | Server-push async generator of `snapshot` / `taskRun` / `closed` frames (`sdk/driver.d.ts:13,403`). |
| `loom.tasks.complete(...)` | `complete-task` | Completes the child `TaskRun` and closes/unblocks the fleet-db task via the `close_task` flag. |
| `loom.tasks.release(taskId)` | `release-task` | Releases the fleet-db task claim held by the driver. |

### Requesting A Child TaskRun

`loom.taskRuns.request(...)` is **asynchronous and enqueue-only**. The SDK
hard-codes `enqueueOnly: true` and `deferCompletion: true` on every call
(`sdk/driver.js:294,308`), so the returned object is the *queued* `TaskRun`, not
a finished one. Execution happens later in serve's task workers
(`driver.TaskWorker`, `internal/driver/task_worker.go:18`).

Consequences an author must plan for:

- **Do not branch on `result.status` immediately after `request`.** Wait for the
  terminal state with `loom.taskRuns.await({ taskRunId })` (polling) or
  `loom.epics.watch({ epicId })` (server push, preferred).
- **Completion is deferred to you.** `request` never closes the fleet-db task.
  Call `loom.tasks.complete(...)` once you decide the child result counts.
- **Pass a deterministic `taskRunId`.** Re-requesting the same id returns a
  `conflict` error, which a restarted workflow should treat as "already in
  flight" (`internal/workflows/builtin/epic-runner.ts:275-302`).

Fields the SDK actually forwards, from `LoomTaskRunRequest`
(`sdk/driver.d.ts:83-112`, built at `sdk/driver.js:278-307`): `taskId`
(required), `taskRunId`, `runner`, `workerProfileId`, `parentSessionId`,
`nodeId`, `runnerId`, `driverStepId`, `capabilities`, `leaseToken`, `repoRef`,
and `input`.

Two traps:

- `runner` is the **runtime strategy selector** — a runner name declared by the
  pinned driver version's manifest, e.g. `local-task-runner` or
  `daytona-task-runner` (`internal/driver/task_bridge.go:29`,
  `internal/driver/bundled_runner.go:20`). It is *not*
  `runnerId`, which identifies the worker process that claims the run.
- `sandboxPlacement` is accepted but **only `repoRef` / `repo_ref` is read from
  it** (`sdk/driver.js:296-300`). Any other key, including `provider`, is
  silently dropped. `providerProfile` and `supportedProviders` exist on the
  server-side wire type (`internal/webui/handlers/driverapi/module.go:514,520`)
  but are not on the SDK type and are not sent by the SDK client.

## Epic Runner Pattern

The claim → enqueue → wait → complete → release policy, written serially. This
is a *teaching* example, not a copy of the builtin: the shipped
`internal/workflows/builtin/epic-runner.ts` is watch-driven, claims up to
`maxConcurrency` (default 2) tasks at once, and does not stop the whole run on
the first failed child (`epic-runner.ts:40-52,80-112`). Read this for call
shapes; read the builtin for the production policy.

Note the `taskRuns.await` step — omitting it is the most common authoring bug,
because `request` returns a queued run, not a finished one.

```ts
export async function run(ctx) {
  const input = ctx.payload || {};
  const loom = createLoomDriverClient({ input });
  const completed = [];

  while (true) {
    const task = await loom.tasks.claimReady({ epicId: input.epicId });
    if (!task) {
      return loom.completed({ summary: 'Epic drained: ' + completed.join(',') });
    }

    // Deterministic id: a restarted run re-requesting the same id gets a
    // `conflict` error, which means "already in flight", not "failed".
    const taskRunId = `${loom.driverRunId}-${task.id}`;
    await loom.taskRuns.request({
      taskId: task.id,
      taskRunId,
      runner: 'local-task-runner',
    });

    // request() only enqueued the run. Wait for a terminal status.
    const result = await loom.taskRuns.await({ taskRunId });

    if (result && result.status === 'completed') {
      await loom.tasks.complete({
        taskId: task.id,
        taskRunId,
        completionId: 'complete-' + taskRunId,
      });
      completed.push(task.id);
      continue;
    }

    await loom.tasks.release(task.id);
    return loom.needsReview({
      summary: 'Task failed: ' + task.id,
      taskRunId,
      logsRef: (result && result.logsRef) || '',
      artifactsRef: (result && result.artifactsRef) || '',
    });
  }
}
```

The explicit workflow decisions are:

- keep looping until no ready task exists;
- request one child task run per claimed task, with a deterministic id;
- wait for the child run to reach a terminal status before judging it;
- close the fleet-db task only when the child `TaskRun` completed;
- release the task and stop the whole run on the first failed child;
- return `needs_review` with enough context to inspect the failure.

`completionId` makes `tasks.complete` idempotent across a restart
(`LoomTaskSelector.completionId`, `sdk/driver.d.ts:76`); the builtin uses the
same `"complete-" + taskRunId` convention (`epic-runner.ts:414-422`).

For anything beyond a serial loop, prefer `loom.epics.watch({ epicId })` over
polling — it is the server-push SSE stream, and the SDK marks `taskRuns.await`
as compat-only (`sdk/driver.d.ts:420-425`).

## Run Inspection APIs

After starting a workflow, use these endpoints to observe it
(`internal/webui/handlers/workflows/module.go:41-43`):

```text
GET /api/workspaces/{ws}/runs/{runId}
GET /api/workspaces/{ws}/runs/{runId}/events
GET /api/workspaces/{ws}/runs/{runId}/stream
```

`GET /runs/{runId}` returns the current `DriverRun` record. `/events` returns
lifecycle events; `/stream` is the SSE version of the same. Child `TaskRun`
records and action-ledger entries live in fleet-db and are linked by
`driver_run_id` or the child `task_run_id`.

## Authoring Rules

- Return an explicit SDK result from every successful code path.
- Treat `ctx.payload` as untrusted JSON and validate required fields.
- Use `needs_review` when the workflow stopped intentionally and another actor
  should decide what happens next.
- Always release claimed tasks if the workflow is not completing them.
- Never judge a child `TaskRun` by the object `taskRuns.request` returned — wait
  on it first.
- Only call `loom.tasks.complete(...)` after the child result has enough
  evidence to close/unblock the fleet-db task.
- Pass a deterministic `taskRunId` and a stable `completionId` so a restarted
  run is idempotent, and treat `conflict` as success.
- Include `taskRunId`, `logsRef`, and `artifactsRef` in review handoffs when
  available.
- Pin the `runner` in the workflow source so the execution policy is auditable
  in the registered version.

## Common Failure Modes

| Symptom | Likely cause |
|---|---|
| Run stays `queued` | No driver executor is running, or `LOOM_DRIVER_EXECUTOR` disabled it. |
| Run fails before TypeScript logs appear | Bundle validation, manifest, digest, or Flue server launch failed. |
| Run fails with `invalid_driver_result` | Workflow did not return an explicit terminal SDK result (`internal/driver/executor.go:349,918,926`). |
| Every child `TaskRun` looks non-`completed` | Code branched on the object `taskRuns.request` returned; that run was still `queued`. |
| SDK call says owner credentials are required | Helper command is not running inside a claimed `DriverRun` environment. |
| Child task completed but fleet-db task stayed open | Workflow requested a `TaskRun` but did not call `loom.tasks.complete(...)`. |
| `POST .../workflows/{name}` returns `400` before any run exists | Runner preflight failed — the agent backend CLI is missing or unauthenticated. `epic-runner` only; other workflow names skip the check. |
| Duplicate POST returns same run | Same `Idempotency-Key` was used. |
| Workflow returns `needs_review` | TypeScript intentionally stopped and requested operator/reviewer/lead-agent review. |

## Related

- [`../loom-glossary.md`](../loom-glossary.md) — driver / driver version /
  driver run / task run / runner / flue / harness, and the three senses of
  "backend"
- [`generic-sse-envelope.md`](generic-sse-envelope.md) — the *browser* SSE
  envelope; the run stream above is a separate endpoint
- [`2026-06-07-trigger-workflow-proposal.md`](2026-06-07-trigger-workflow-proposal.md)
  — how an inbound webhook becomes a `DriverRun` instead of an API call
- [`agent-run-visibility-plan.md`](agent-run-visibility-plan.md) — the operator
  view over runs
- [`2026-07-23-control-plane-as-built.md`](2026-07-23-control-plane-as-built.md)
  — leases, fencing tokens, and node identity behind the platform-owned column
