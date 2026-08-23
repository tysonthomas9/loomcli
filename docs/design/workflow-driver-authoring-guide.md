# Workflow Driver Authoring Guide

**Status:** current platform contract
**Audience:** developers and agents writing TypeScript workflows for Loom

This guide describes how a registered TypeScript workflow runs through Loom,
FleetDB, and Flue. The key distinction is:

- `DriverRun` is platform-owned lifecycle and durability.
- The `.ts` workflow is user-owned policy and orchestration.

The platform should make the run reliable. The TypeScript should decide what
work means.

## Core Terms

| Term | Meaning |
|---|---|
| `Driver` | A named TypeScript program registered with Loom/FleetDB. |
| `DriverVersion` | One immutable built artifact for a driver. Runs are pinned to versions. |
| `DriverRun` | One execution request for a driver version, created from an API request, CLI command, trigger, or schedule. |
| `TaskRun` | One auditable execution attempt for a single FleetDB task. |
| `ActionLedger` | Idempotent record of an external side effect, such as `close_task`. |

## How A Workflow Starts

The simple HTTP path is:

```text
POST /api/workspaces/{ws}/workflows/{name}
  -> create queued DriverRun
  -> driver executor claims it
  -> executor launches the pinned Flue bundle
  -> Flue invokes workflows/{name}.ts run(ctx)
  -> workflow explicitly returns completed | failed | needs_review
  -> executor finishes the DriverRun
```

`POST /api/workspaces/{ws}/workflows/{name}` does not directly execute the
TypeScript in the request handler. It records durable work. A driver executor
then picks up queued runs. `loom serve` enables the DriverRun executor by
default; set `LOOM_DRIVER_EXECUTOR=0`, `false`, `off`, or `no` to leave runs
queued.

If no executor is running, the API can still return `202 Accepted`, but the run
will remain `queued`.

## Registration And Versioning

Before a workflow can run, it must have an active passed `DriverVersion`.

The API registration surface is:

```text
POST /api/workspaces/{ws}/workflows/{name}/versions
```

Request shape:

```json
{
  "entrypoint": "workflows/epic-runner.ts",
  "activate": true,
  "files": {
    "workflows/epic-runner.ts": "export async function run(ctx) { return { status: 'completed' }; }"
  }
}
```

The server validates the file set, builds it with Flue, registers the resulting
bundle as a `DriverVersion`, and activates it when requested. A later run uses
the active version at run creation time. Editing source after registration does
not change an already registered version.

`activate` **defaults to `false`**: registering a version no longer silently
switches the active one. Activate explicitly, in a separate step:

```text
POST /api/workspaces/{ws}/workflows/{name}/versions/{id}/activate
loom workflow activate <name> --version <id>
```

Built-in workflows, such as `epic-runner`, are registered lazily the first time
they are invoked if no user-registered workflow with that name exists.

### Built-in versioning, update & rollback

A **built-in** workflow (`epic-runner`, `github-review-agent`) is versioned by
its packaged bundle and follows an **update track**. See
`docs/design/2026-08-22-workflow-versioning-rollback.md` for the full model
(decisions D1–D7); the operator surface:

```text
loom workflow sync <name>                  # register this build's packaged version
                                           # + apply track policy (auto by default)
loom workflow activate <name> --builtin    # adopt packaged version + follow updates (auto)
loom workflow activate <name> --version V [--track auto|pinned]  # --track auto only if V is packaged
loom workflow rollback <name> [--version V]# default target: recorded previous active; pins the track
loom workflow versions <name> [--json]     # newest-first; provenance / selected_by /
                                           # bundle_verified per version + built-in update block
```

On the **`auto`** track an app update (or downgrade) re-activates the newly
packaged version automatically. On the **`pinned`** track — set by an explicit
`activate`/`rollback`, or implied by an active *custom* (authored) version — the
active version is preserved and the packaged one is surfaced as
`update_available`. The same operations exist over HTTP under
`/api/workspaces/{ws}/workflows/{name}/` (`builtin/sync`, `rollback`,
`versions/{id}/activate`).

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
| Invocation | The executor starts the built Flue Node server and sends `{ payload }` to `run(ctx)`. |
| Runtime errors | Build, bundle, process, and invocation errors become failed `DriverRun`s. |
| Result validation | Missing, malformed, or non-terminal workflow results become failed `DriverRun`s with `invalid_driver_result`. |
| Cancellation | Context cancellation maps to `cancelled`. |
| Finalization | The executor persists status, summary, error class, output metadata, and log refs. |
| Events | Run lifecycle events are available through run event APIs. |

The run status lifecycle is:

```text
queued -> running -> completed | failed | needs_review | cancelled
```

`needs_review` means the workflow intentionally stopped and returned control to
an operator, reviewer, or lead agent. It is not limited to human review.

## Workflow-Owned Explicit Logic

The `.ts` file owns workflow policy. If behavior should vary by workflow, put it
in TypeScript.

| Decision | Owned by `.ts` |
|---|---|
| Payload interpretation | Read `ctx.payload` and choose input semantics. |
| Task selection | Decide whether to claim one task, all ready tasks, or none. |
| Ordering | Decide sequence, dependency strategy, prioritization, or batching. |
| Concurrency | Decide whether to run child task attempts serially or in parallel. |
| Provider choice | Choose `providerProfile`, supported providers, and sandbox placement. |
| Success policy | Decide what child `TaskRun` result is acceptable. |
| Task completion | Call `loom.tasks.complete(...)` only when the task should close/unblock. |
| Release policy | Call `loom.tasks.release(...)` when the workflow gives up a claimed task. |
| Review handoff | Return `loom.needsReview(...)` with useful task/log/artifact context. |
| Final result | Return `loom.completed(...)`, `loom.failed(...)`, or `loom.needsReview(...)`. |

The platform does not infer that an epic is drained, that a failed task should
release its claim, or that a successful child task should close a FleetDB task.
Those are workflow decisions.

## Minimal Workflow Shape

```ts
import { createLoomDriverClient } from '@loom/sdk/flue';

export async function run(ctx) {
  const input = ctx.payload || {};
  const loom = createLoomDriverClient({ input });

  console.log('workflow-start ' + (input.epicId || ''));

  return loom.completed({ summary: 'done' });
}
```

The final value returned from `run(ctx)` is the `DriverRun` result. Prefer the
SDK helpers so status strings stay consistent:

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

## FleetDB Task Execution From A Workflow

The driver SDK exposes helper methods that call back into Loom's hidden driver
commands. These commands carry the current `DriverRun` owner credentials through
environment variables, so the TypeScript code does not need to pass leases
manually.

| SDK call | Platform side effect |
|---|---|
| `loom.tasks.claimReady({ epicId })` | Claims one ready FleetDB task for `driver-run:{runId}`. |
| `loom.taskRuns.request({...})` | Creates and claims a child `TaskRun`, runs the configured executor, records logs/artifacts/status, and returns the result. |
| `loom.tasks.complete(...)` | Completes the child `TaskRun` through FleetDB and closes/unblocks the task with `ActionLedger close_task`. |
| `loom.tasks.release(taskId)` | Releases the FleetDB task claim held by the driver. |

`loom.taskRuns.request(...)` does not close the FleetDB task by itself when the
workflow uses deferred completion. The workflow must explicitly call
`loom.tasks.complete(...)` after deciding the child result should count.

## Epic Runner Pattern

The built-in `epic-runner` uses this policy:

```ts
import { createLoomDriverClient } from '@loom/sdk/flue';

export async function run(ctx) {
  const input = ctx.payload || {};
  const loom = createLoomDriverClient({ input });
  const completed = [];

  while (true) {
    const task = await loom.tasks.claimReady({ epicId: input.epicId });
    if (!task) {
      return loom.completed({ summary: 'Epic drained: ' + completed.join(',') });
    }

    const result = await loom.taskRuns.request({
      taskId: task.id,
      providerProfile: 'flue-local',
      supportedProviders: ['flue-local'],
      sandboxPlacement: { provider: 'flue-local' },
    });

    if (result.status === 'completed') {
      await loom.tasks.complete({
        taskId: task.id,
        taskRunId: result.taskRunId || result.id,
      });
      completed.push(task.id);
      continue;
    }

    await loom.tasks.release(task.id);
    return loom.needsReview({
      summary: 'Task failed: ' + task.id,
      taskRunId: result.id,
      logsRef: result.logsRef || '',
      artifactsRef: result.artifactsRef || '',
    });
  }
}
```

The explicit workflow decisions are:

- keep looping until no ready task exists;
- request one child task run per claimed task;
- close the FleetDB task only when the child `TaskRun` completed;
- release the task and stop the whole run on the first failed child;
- return `needs_review` with enough context to inspect the failure.

## Run Inspection APIs

After starting a workflow, use these endpoints to observe it:

```text
GET /api/workspaces/{ws}/runs/{runId}
GET /api/workspaces/{ws}/runs/{runId}/events
GET /api/workspaces/{ws}/runs/{runId}/stream
```

`GET /runs/{runId}` returns the current `DriverRun` record. The event endpoints
return or stream lifecycle events. Child `TaskRun` records and `ActionLedger`
entries live in FleetDB and are linked by `driver_run_id` or the child
`task_run_id`.

## Authoring Rules

- Return an explicit SDK result from every successful code path.
- Treat `ctx.payload` as untrusted JSON and validate required fields.
- Use `needs_review` when the workflow stopped intentionally and another actor
  should decide what happens next.
- Always release claimed tasks if the workflow is not completing them.
- Only call `loom.tasks.complete(...)` after the child result has enough
  evidence to close/unblock the FleetDB task.
- Include `taskRunId`, `logsRef`, and `artifactsRef` in review handoffs when
  available.
- Prefer provider profiles and sandbox placement in the workflow source so the
  execution policy is auditable in the registered version.

## Common Failure Modes

| Symptom | Likely cause |
|---|---|
| Run stays `queued` | No driver executor is running, or `LOOM_DRIVER_EXECUTOR` disabled it. |
| Run fails before TypeScript logs appear | Bundle validation, manifest, digest, or Flue server launch failed. |
| Run fails with `invalid_driver_result` | Workflow did not return an explicit terminal SDK result. |
| SDK call says owner credentials are required | Helper command is not running inside a claimed `DriverRun` environment. |
| Child task completed but FleetDB task stayed open | Workflow requested a `TaskRun` but did not call `loom.tasks.complete(...)`. |
| Duplicate POST returns same run | Same idempotency key was used. |
| Workflow returns `needs_review` | TypeScript intentionally stopped and requested operator/reviewer/lead-agent review. |
