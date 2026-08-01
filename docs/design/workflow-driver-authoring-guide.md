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
| Runner | A named execution strategy declared by a `DriverVersion` manifest and selected per child `TaskRun`. |
| Await instance | The durable row behind one `loom.events.await` / `loom.workflows.await` call, keyed `runId#await-{n}`. |

## How A Workflow Starts

The simple HTTP path is:

```text
POST /api/workspaces/{ws}/workflows/{name}
  -> create queued DriverRun, storing the raw JSON body as the run payload
  -> driver executor claims it
  -> executor exports the payload as LOOM_FLUE_INVOKE_PAYLOAD and launches
     the pinned Flue bundle with FLUE_INTERNAL_CLI_IPC=1
  -> the bundle runs the module's default-exported defineWorkflow()
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
  "entrypoint": "workflows/my-workflow.ts",
  "activate": true,
  "files": {
    "workflows/my-workflow.ts": "<the module source; see Minimal Workflow Shape>"
  }
}
```

The server validates the file set, builds it with Flue, registers the resulting
bundle as a `DriverVersion`, and activates it when requested. A later run uses
the active version at run creation time. Editing source after registration does
not change an already registered version.

This endpoint accepts `files`, `entrypoint`, and `activate` only. It does not
accept a runner list, and it does not derive one — see
[Runners And The Pinned Manifest](#runners-and-the-pinned-manifest) for why
that matters and which path does declare runners.

Built-in workflows, such as `epic-runner`, are registered lazily the first time
they are invoked if no user-registered workflow with that name exists.

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
| Stale recovery | A run whose heartbeat expired (~5 minutes) is transitioned to `failed` with `stale_driver_run`. It is not requeued. |
| Bundle safety | The executor loads the pinned bundle, verifies manifest refs, and checks the bundle digest. |
| Invocation | The executor exports the payload as `LOOM_FLUE_INVOKE_PAYLOAD`, forks the built Flue Node server in one-shot IPC mode, and reads the result off the last stdout line. |
| Runtime errors | Build, bundle, process, and invocation errors become failed `DriverRun`s. |
| Result validation | Missing, malformed, or non-terminal workflow results become failed `DriverRun`s with `invalid_driver_result`. |
| Durable await | `loom.events.await` / `loom.workflows.await` suspend the run server-side, release the executor slot, and re-queue it when the event or the timeout lands. |
| Cancellation | Context cancellation maps to `cancelled`. |
| Finalization | The executor persists status, summary, error class, output metadata, and log refs. |
| Events | Run lifecycle events are available through run event APIs. |

The run status lifecycle is:

```text
queued -> running -> completed | failed | needs_review | cancelled
              |
              +-> suspended_awaiting_event -> queued -> running -> ...
```

`needs_review` means the workflow intentionally stopped and returned control to
an operator, reviewer, or lead agent. It is not limited to human review.

`suspended_awaiting_event` is not terminal and not an error: the run is alive
with no process attached, waiting for its await to resolve. It keeps the same
`RunID` when it resumes.

## Workflow-Owned Explicit Logic

The `.ts` file owns workflow policy. If behavior should vary by workflow, put it
in TypeScript.

| Decision | Owned by `.ts` |
|---|---|
| Payload interpretation | Parse `LOOM_FLUE_INVOKE_PAYLOAD` and choose input semantics. |
| Task selection | Decide whether to claim one task, all ready tasks, or none. |
| Ordering | Decide sequence, dependency strategy, prioritization, or batching. |
| Concurrency | Decide whether to run child task attempts serially or in parallel. |
| Runner choice | Choose the `runner` name for each child `TaskRun` from the pinned manifest. |
| Success policy | Decide what child `TaskRun` result is acceptable. |
| Replay safety | Skip or re-derive already-completed side effects when the run re-executes. |
| Task completion | Call `loom.tasks.complete(...)` only when the task should close/unblock. |
| Release policy | Call `loom.tasks.release(...)` when the workflow gives up a claimed task. |
| Review handoff | Return `loom.needsReview(...)` with useful task/log/artifact context. |
| Final result | Return `loom.completed(...)`, `loom.failed(...)`, or `loom.needsReview(...)`. |

The platform does not infer that an epic is drained, that a failed task should
release its claim, or that a successful child task should close a FleetDB task.
Those are workflow decisions.

## Minimal Workflow Shape

A workflow module must **default-export a `defineWorkflow()` definition**. A
bare `export function run` is not an entrypoint: the current Flue runtime no
longer normalizes it, so a module without the default export has nothing to
invoke and fails before any of your code runs.

The invocation payload does **not** arrive through Flue's input channel. The
launcher passes it out of band in the environment, and the workflow reads it
itself:

- `LOOM_FLUE_INVOKE_PAYLOAD` — the `DriverRun` payload, set by the driver
  executor when it launches the bundle.
- `LOOM_TASK_RUN_REQUEST_JSON` — the `TaskRun` request, set by the task-run
  host bridge when the same bundle is invoked as a runner.

Read both, in that order, so one module works on either path.

```ts
import { createLoomDriverClient } from '@loom/sdk/driver';
import { defineAgent, defineWorkflow } from '@flue/runtime';

// The bound agent is a credential-free stub: this workflow orchestrates
// through the Loom driver SDK, it is not an LLM agent, so it declares no
// model and consumes no harness credentials.
export default defineWorkflow({
  agent: defineAgent(() => ({ model: false })),
  run: async () => toJsonResult(await run({ payload: invokePayload() })),
});

function invokePayload() {
  const raw = process.env.LOOM_FLUE_INVOKE_PAYLOAD
    || process.env.LOOM_TASK_RUN_REQUEST_JSON
    || '{}';
  try {
    return JSON.parse(raw);
  } catch {
    return {};
  }
}

// The runtime validates the returned value with a strict JSON check that
// rejects undefined/function/symbol/bigint. Round-tripping through JSON
// drops undefined instead of throwing, so optional result fields left unset
// (needsReview's taskRunId/logsRef/artifactsRef) are safe.
function toJsonResult(value) {
  return value === undefined ? null : JSON.parse(JSON.stringify(value));
}

export async function run(ctx) {
  const input = ctx.payload || {};
  const loom = createLoomDriverClient({ input });

  console.log('workflow-start ' + (input.epicId || ''));

  return loom.completed({ summary: 'done' });
}
```

Keeping the policy in a plain `run(ctx)` and wiring it up in the default
export is a convention, not a requirement. It is what the built-in workflows
do, so the same function stays directly testable without the runtime.

Two environment details are load-bearing and owned by the platform, not by you:
the launcher sets `FLUE_INTERNAL_CLI_IPC=1` (without it the generated entry
serves HTTP instead of performing the invoke/result handshake), and it still
sends a legacy `payload` field over IPC that the runtime ignores. That is why
the payload has to come from the environment.

## The Return Contract

The value returned from the workflow is the `DriverRun` result. Prefer the SDK
helpers so status strings stay consistent:

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

This is enforced, not advisory. The executor passes every workflow result
through a terminal-status check before finishing the run. Anything that is not
`completed`, `failed`, `needs_review`, or `cancelled` is rewritten to `failed`
with `error_class = "invalid_driver_result"` and a summary naming the offending
status. That covers falling off the end of the function, returning
`{ summary: 'done' }`, returning `{}`, and returning a non-terminal status such
as `running`. Empty stdout and undecodable stdout fail the same way.

There is exactly one non-terminal status the check lets through:
`suspended_awaiting_event`. It is the clean exit after a durable await
suspended the run server-side — the executor acknowledges it instead of
finishing the run. Two rules follow:

- Never fabricate it. If the workflow reports suspended while the run is still
  `running` under the executor's own lease, no await actually suspended it, and
  the executor finishes the run `failed` with `invalid_driver_result` rather
  than leaking the slot.
- Never swallow the suspend sentinel. `loom.events.await` and
  `loom.workflows.await` throw `WorkflowSuspended` when the server suspends the
  run. A `try`/`catch` around an await must rethrow when
  `isWorkflowSuspended(err)` is true, or return `err.result`, which is
  exactly the suspended shape.

## FleetDB Task Execution From A Workflow

The driver SDK exposes helper methods that call back into Loom's hidden driver
commands. These commands carry the current `DriverRun` owner credentials through
environment variables, so the TypeScript code does not need to pass leases
manually.

| SDK call | Platform side effect |
|---|---|
| `loom.tasks.claimReady({ epicId })` | Claims one ready FleetDB task for `driver-run:{runId}`. |
| `loom.taskRuns.request({...})` | Creates a child `TaskRun` and **enqueues** it. A serve task worker executes it and records logs/artifacts/status. |
| `loom.taskRuns.get({ taskRunId })` | Reads one child `TaskRun` belonging to this driver run. |
| `loom.epics.watch({ epicId })` | Server-push SSE stream of epic and child `TaskRun` events. |
| `loom.tasks.complete(...)` | Completes the child `TaskRun` through FleetDB and closes/unblocks the task with `ActionLedger close_task`. |
| `loom.tasks.release(taskId)` | Releases the FleetDB task claim held by the driver. |

`loom.taskRuns.request(...)` is enqueue-only and defers completion: it returns
as soon as the child run is created, not when it finishes, and it never closes
the FleetDB task by itself. Observe the outcome through `loom.epics.watch(...)`
(push) or `loom.taskRuns.get(...)`, then explicitly call
`loom.tasks.complete(...)` once the child result should count.

## Determinism And Replay

This is the easiest way to write a workflow that looks correct and is not. Read
this section before writing anything that awaits or has external side effects.

**There is no step journal.** The platform does not memoize your function calls.
The only durable resume point is an await boundary, and on resume the workflow
function **re-executes from the top** — same `RunID`, same payload, fresh
process, no memory of the previous pass. Everything before the await runs
again.

What is durable is the await ledger: each await registers a row keyed
`runId#await-{n}`, and a resolved await replays its recorded event inline
instead of suspending again. That replay is the entire resume mechanism.

### Awaits Are Keyed By Call Order

`{n}` is the ordinal of the await *call*, taken from a per-process counter that
starts at 1 on every entry. The instance key is derived server-side from the
authenticated run identity, so it cannot be forged — but it also means the
mapping from "the nth await call" to "which recorded event you get back" is
positional.

The consequence: **awaits must never be conditionally skipped or reordered
across re-entries.** If the first pass awaits A then B, and the re-entered pass
skips A because some state changed, the workflow will read A's recorded event
where it expected B's. Nothing detects this at runtime. It is a rule you keep,
not a rule the platform enforces.

Practically: hoist awaits out of branches whose condition can change between
entries, and never put an await behind a check on mutable external state. The
same positional rule governs `loom.workflows.start` without an explicit
`idempotencyKey` (a per-process start counter) and connector `callSeq` (a
per-action counter).

`loom.events.list()` returns the run's awaits in index order, with their
recorded events, and consumes no await slot — use it to rebuild context on
re-entry rather than re-deriving it from side effects.

### Skipping Completed Side Effects Is Your Job

Re-execution replays your side effects unless you make them idempotent. The
tools the platform gives you are identity, not memoization:

| Mechanism | What it makes idempotent |
|---|---|
| Deterministic child `taskRunId` | A re-entered run re-derives the same id, so re-requesting the same child conflicts instead of duplicating. |
| `completionId` on `loom.tasks.complete` | A genuine deferred completion replayed on re-entry stays one completion. |
| Connector `callSeq` | The server derives the call id and provider idempotency key from `(runId, action, callSeq)`, so the same call in the same order dedupes upstream. |
| Explicit `idempotencyKey` on `loom.workflows.start` | Pins child-run identity independently of call order. |

Derive the child id from data you already have, so it survives re-entry:

```ts
function deterministicTaskRunId(driverRunId, taskId) {
  return 'task-run-' + slug(driverRunId) + '-' + slug(taskId);
}
```

Then treat conflicts as success. When a replayed call hits an object that
already exists, or a state machine that has already moved on, that is the
already-done signal — not a failure:

```ts
function isConflictError(err) {
  switch (err && err.code) {
    case 'conflict':
    case 'already_exists':
    case 'invalid_transition':
      return true;
    default:
      return false;
  }
}
```

A workflow that treats these as errors will fail every time it resumes.

### State Read Before An Await Is Stale After It

A suspend can last days. By default a single await may specify a timeout of up
to 14 days, and a run's cumulative suspended time is capped at 30 days.
Anything you read before the await may have changed by the time it returns, and
the recorded event tells you only what happened, not what is true now.

Re-check freshness after every await, before acting on it:

```ts
const approval = await loom.events.await({
  pattern: 'approval:octo/hello#123@' + headSha,
  actor: eligibleApprovers,
  timeoutMs: 7 * 24 * 3600e3,
});
if (approval.status === 'timed_out') {
  return loom.needsReview({ summary: 'approval window expired' });
}

// The approval was for headSha. Confirm it is still the head before acting.
const pr = await loom.connectors.github.readPullRequest({ /* ... */ });
if (pr.body.head.sha !== headSha) {
  return loom.failed({ summary: 'subject moved while suspended' });
}
```

A timed-out await returns normally with `status: "timed_out"` and a synthetic
timeout event. It does not throw. Branch on `status`.

### A Crash Outside An Await Window Is Not Resumed

Suspension is the only path back. If the process dies while the workflow is
running — panic, OOM, node loss — nothing re-enters it. The run stops
heartbeating, and after roughly five minutes the stale-run sweeper transitions
it straight to `failed` with `error_class = "stale_driver_run"`. There is no
requeue and no retry.

Recovery means creating a **new** `DriverRun`, with a new `RunID`. Everything
keyed off the run id changes with it: deterministic child task-run ids,
connector call ids, await instance keys. The new run will not conflict with the
old run's side effects, so it will redo them. Design child work so a second
attempt is safe, or make the workflow reconcile against a fresh snapshot before
it acts.

## Runners And The Pinned Manifest

`loom.taskRuns.request({ runner })` selects a runtime strategy by name. That
name is resolved against the **runner manifest of the workflow's own pinned
`DriverVersion`** — the version the run pinned at creation, belonging to the
same driver. A name the manifest does not declare is rejected before the child
task run is created:

```text
runner "local-task-runner" is not declared by driver version "..."
```

The manifest is never fabricated. If a registration supplies no runner specs,
the version is registered with an empty runner list rather than defaulting to a
plausible set that may not exist in the bundle.

Auto-derivation — scanning `workflows/*.ts` siblings of the entrypoint and
declaring each as a runner — is **built-in only**. It is reserved for trusted
source-tree registration of the embedded workflows. Custom registrations do not
get it, which means sibling workflow files in a custom bundle stay
bundle-private: shipped in the artifact, not selectable as runners.

So a custom workflow that wants to route child work to a runner must do two
things:

1. **Copy the runner source into its own `workflows/` directory.** A runner is
   resolved out of the requesting workflow's own bundle; it is not borrowed
   from the built-in `epic-runner` version, even for a runner with the same
   name.
2. **Declare it in `workflow.json` under `runners`.**

```json
{
  "schema_version": "1",
  "driver_id": "my-workflow",
  "entrypoint": "workflows/my-workflow.ts",
  "runners": [
    {
      "name": "local-task-runner",
      "kind": "flue-workflow",
      "entrypoint": "local-task-runner"
    }
  ]
}
```

`kind` must be `flue-workflow` or `node-module`. `entrypoint` must be
relative and free of path traversal. `openshell-task-runner` is denied at
resolve time: it is an unimplemented fail-closed stub and can never be
selected.

`loom workflow clone <builtin> --out <dir>` writes both halves for you: the
built-in's sources under `workflows/` and its derived runner list in
`workflow.json`. `loom workflow build <name> --source <dir>` then carries that
list into the registered version.

The HTTP registration endpoint has no `runners` field. A version registered
that way has an empty runner manifest, and every `runner`-carrying
`taskRuns.request` from it will be rejected. Use the CLI source path when the
workflow needs runners.

## Epic Runner Pattern

The built-in `epic-runner` is a watch-driven drain loop, and it is a useful
model for re-entrant orchestration:

- claim ready tasks up to `maxConcurrency` and enqueue a child `TaskRun` for
  each, with a deterministic task-run id;
- consume `loom.epics.watch(...)` for terminal child events and top the
  pipeline back up — no polling cadence, no per-batch barrier;
- complete finished children with a `completionId`, treating conflicts as
  already-done, since the serve task worker closes successful runs itself;
- release a claimed task if the enqueue fails before anything is running,
  rather than stranding it until the lock TTL expires;
- re-derive in-flight state from a fresh epic snapshot plus the watch handshake
  on re-entry, instead of assuming a previous pass left memory behind.

The explicit workflow decisions are: how many tasks to run at once, what a
successful child result is, when a FleetDB task should be closed, and whether a
blocked task ends the run or is reported for review.

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

- Default-export a `defineWorkflow()` definition. A bare `export function run`
  is not an entrypoint.
- Read the payload from `LOOM_FLUE_INVOKE_PAYLOAD` (falling back to
  `LOOM_TASK_RUN_REQUEST_JSON`), treat it as untrusted JSON, and validate
  required fields.
- Return an explicit SDK result from every successful code path.
- Assume the function will re-execute from the top. Make every side effect
  idempotent by identity, and treat conflict / already-exists /
  invalid-transition as already-done.
- Never conditionally skip or reorder awaits between entries.
- Re-check freshness after every await before acting on the event.
- Rethrow the suspend sentinel — never let a `catch` around an await swallow
  it.
- Use `needs_review` when the workflow stopped intentionally and another actor
  should decide what happens next.
- Always release claimed tasks if the workflow is not completing them.
- Only call `loom.tasks.complete(...)` after the child result has enough
  evidence to close/unblock the FleetDB task.
- Include `taskRunId`, `logsRef`, and `artifactsRef` in review handoffs when
  available.
- Keep the runner choice in the workflow source so the execution policy is
  auditable in the registered version, and declare every runner it names.

## Common Failure Modes

| Symptom | Likely cause |
|---|---|
| Run stays `queued` | No driver executor is running, or `LOOM_DRIVER_EXECUTOR` disabled it. |
| Run fails before TypeScript logs appear | Bundle validation, manifest, digest, or Flue server launch failed. |
| Run fails with `driver_runtime` and no workflow output | Module has no default-exported `defineWorkflow()`, so the runtime found nothing to invoke. |
| Run fails with `invalid_driver_result` | Workflow did not return an explicit terminal SDK result, or returned nothing at all. |
| Run fails with `invalid_driver_result`, summary mentions suspension | Workflow reported suspended while still owning its lease; no await suspended it. |
| Payload is empty inside the workflow | Workflow read Flue's input channel instead of `LOOM_FLUE_INVOKE_PAYLOAD`. |
| `runner "x" is not declared by driver version` | Runner is missing from the pinned version's manifest — copy it into `workflows/` and declare it in `workflow.json`. |
| Run fails with `stale_driver_run` | Process died outside an await window; heartbeats stopped and the sweeper terminalized it. There is no requeue. |
| Duplicate side effects after a resume | Side effect had no deterministic id, or a conflict error was treated as a failure. |
| Resumed run reads the wrong await event | Awaits were skipped or reordered between entries, shifting the call-order index. |
| SDK call says owner credentials are required | Helper command is not running inside a claimed `DriverRun` environment. |
| Child task completed but FleetDB task stayed open | Workflow requested a `TaskRun` but did not call `loom.tasks.complete(...)`. |
| Duplicate POST returns same run | Same idempotency key was used. |
| Workflow returns `needs_review` | TypeScript intentionally stopped and requested operator/reviewer/lead-agent review. |
