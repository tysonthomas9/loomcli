# Epic Runner Workflow Architecture

> **Status:** Current · *audited 2026-07-23*
>
> How an epic actually drains today. This doc replaces the mechanism half of
> [epic-runner-lead-control.md](epic-runner-lead-control.md), whose Go
> in-process runner was deleted in `553998a1d` (2026-06-09). The *product*
> rules — one epic per lead, conflict outcomes, ownership survives draining —
> are owned by
> [docs/product/lead-agent-epic-runner-spec.md](../product/lead-agent-epic-runner-spec.md);
> this doc says how they are enforced.

**Written:** 2026-07-23

## Shape

An epic run is a **driver run of a Flue workflow**, not a Go loop.

```text
loom epic run --parent EPIC-1        UI "Run Epic" button
        │                                    │
        │  runtime preflight (fail-closed)   │  runtime preflight (fail-closed)
        ▼                                    ▼
   CreateDriverRun(driver = epic-runner workflow, payload{epicId, leadName, …})
        │
        ▼
   driver executor runs the bundle → internal/workflows/builtin/epic-runner.ts
        │
        ├─ startEpicRun:  validate epic, resolve + bind lead, deliver assignment
        └─ runEpicWatchLoop:  claim ready task → enqueue TaskRun → watch → repeat
                                    │
                                    ▼
                        serve task workers execute each TaskRun
```

Nothing in this path creates an agent for child work. Child work is a
`domain.TaskRun` (`internal/domain/platform.go:498`), claimed and executed by
serve's task-worker pool.

## Entry points

Both entry points converge on one `DriverRun`.

| Path | Code | Notes |
|---|---|---|
| CLI | `internal/cli/epic/run.go:167` (`queueEpicWorkflowRun` at `:192`, `driver.CreateDriverRun` at `:200`) | Executes the run in-process afterwards (`executeWorkflowRun`, `:292`) unless `--detach` (`:173`). |
| Web UI | `internal/webui/frontend/src/hooks/workspace/startEpicRunnerForIssue.ts:136` → `POST /api/workspaces/{ws}/workflows/{name}` (route `internal/webui/handlers/workflows/module.go:40`, handler `createWorkflowRun` at `:131`) | Returns `202` with the run; execution is left to serve's driver executor. |

The workflow name defaults to `workflowdefs.BuiltinEpicRunnerWorkflowName`;
`--workflow` runs any registered workflow with the same payload shape
(`internal/cli/epic/run.go:81`). `ensureWorkflow` (`:283`) self-heals the
builtin registration on first use.

### The UI mints a lead per epic

`startEpicRunnerForIssue` does **not** bind the currently selected lead. It
creates a fresh agent named `lead-<epic-slug>` with `role_name: "lead"`
(`nextEpicLeadName` at `startEpicRunnerForIssue.ts:33-47`, creation at
`:112-118`, retrying up to five times on name conflict at `:107-127`), starts
the workflow with it (`:136-141`), and deletes the agent if the workflow start
fails (`:144-152`). Consequence: the one-epic-per-lead conflict below is
rarely reachable from the UI — it mostly guards the `loom epic run` path.
Whether one-lead-per-epic or one-lead-per-run is the intent is still an open
product question (see the lead spec's *UI Run Epic Button*).

### Fail-closed runner preflight

Both paths preflight the **local task runner** before the run row exists,
because the local runner shells out to the resolved agent-backend CLI and a
missing binary or missing auth would otherwise fail deep inside a worker — or
worse, fake-complete.

- UI: `preflightRunnerForRun` (`internal/webui/handlers/workflows/preflight.go:23-31`),
  called at `internal/webui/handlers/workflows/module.go:157` *before*
  `CreateDriverRun`.
- CLI: `internal/cli/epic/run.go:131-135`.

Both gate only when the runner resolves to local. An absent `runner` field is
the UI "Locally" default and is treated as local
(`runnerIsLocal`, `preflight.go:36-39`; `runnerNeedsLocalPreflight`,
`internal/cli/epic/run.go:99-102`). Explicit non-local runners (daytona) bring
their own runtime and are not gated.

## Start: validation and lead binding

`startEpicRun` (`internal/workflows/builtin/epic-runner.ts:453-566`) is the
single place lead↔epic rules are enforced. In order:

| Check | Failure `errorClass` | Code |
|---|---|---|
| epic exists | `epic_not_found` | `:456-462` |
| issue is type `epic` | `invalid_epic` | `:463-470` |
| no `leadName` in payload → run unassigned, skip the rest | — | `:472-480` |
| lead agent exists | `lead_not_found` | `:482-490` |
| lead role is `lead` or `orchestrator` | `invalid_lead_role` | `:491-498` |
| lead's `parent` is empty or already this epic | `lead_already_running_epic` | `:500-507` |
| no *other* lead already owns this epic | `epic_already_claimed` | `:509-516` |

The conflict message is verbatim at `:505`:
`"lead <name> is already running epic <other>; clear or finish that epic before running <this>"`.
`findConflictingLeadOwner` (`:602-614`) scans every agent for another lead
whose `parent` equals this epic. `isLeadRole` (`:616-624`) is the TypeScript
twin of `epicrunner.IsLeadRole` (`internal/epicrunner/start.go:71-79`) — both
match the literal strings `lead` and `orchestrator`, case-insensitively.

Binding is a compare-and-set: `loom.agents.updateParent({agent, parent,
expectParent})` (`:545-556`) is served by `updateAgentParent`
(`internal/webui/handlers/driverapi/module.go:404-437`), which takes
`epicrunner.AcquireBindLock` (`internal/epicrunner/start.go:82`) and rejects
with `ErrConflict` when the observed parent moved (`module.go:421,430-431`).
That lock is an advisory **flock** (`lockfile.TryLockExclusive`,
`start.go:111`) on a file under `~/.loom/epic-runner-locks`, so it serializes
across *processes* on one host, not just goroutines. It is keyed by
**workspace**, not by lead (`sanitizeLockName(workspace)`, `start.go:96,100`) —
every ownership change in a workspace queues behind the same lock. `agent.parent` is the authoritative lock; there is no
`LeadAssignment` record.

Already-bound to the same epic is a **resume**, not an error (`:523-533`).
`--dry-run` stops after validation (`epic-runner.ts:71-75`,
`internal/cli/epic/run.go:160-165`).

## Drain: the watch loop

`runEpicWatchLoop` (`internal/workflows/builtin/epic-runner.ts:80-203`) is
edge-triggered. There is no polling cadence and no per-batch barrier, despite
`--interval-seconds` still existing as a flag
(`internal/cli/epic/run.go:78`) and being passed in the payload (`:239`) —
the workflow ignores it.

1. **Seed.** `topUp()` (`:97-111`) calls `loom.tasks.claimReady({epicId})` and
   `enqueueChildTask` for each claimed task until `inFlight.size` reaches
   `maxConcurrency` (default 2, `:81`).
2. **Connect.** `for await (const event of loom.epics.watch({epicId}))`
   (`:129`). Frame types are frozen SDK v1: `snapshot` → `taskRun`* →
   `closed` (`sdk/driver.d.ts:9-13`).
3. **Handshake snapshot** (`:136-149`) reconciles `inFlight` against the
   server's active-run list (`reconcileInFlight`, `:248-266`) — this is how a
   restarted run re-adopts children it did not enqueue in this process — then
   evaluates end state, tops up, and re-checks.
4. **Journal events** (`:150-194`). Events not belonging to this driver run are
   ignored (`:156-158`). `taskRunCompleted` → `completeChildTask`;
   `taskRunFailed` / `taskRunCancelled` → record a blocked failure and keep
   draining independent DAG branches (the server scheduler already exhausted
   retries by then). Then delete from `inFlight`, top up, re-check end state.
5. A `closed` frame fails the run with `epic_watch_closed` (`:130-135`);
   falling out of the iterator without one fails with `epic_watch_ended`
   (`:199-202`).

### Terminal conditions

`endStateResult` (`:208-242`) only fires when nothing is active:

- no open children and no blocked failures → `completed`, "Epic drained …"
- ready work remains → keep running
- blocked failures recorded → `needsReview`, `epic_tasks_blocked`
- children blocked, none ready → `needsReview`, `epic_blocked`
- children open but none ready/blocked/active → `needsReview`,
  `epic_no_progress`

### Re-entrancy

The loop is deliberately naive and restart-tolerant:

- Task-run ids are deterministic: `task-run-<slug(driverRunId)>-<slug(taskId)>`
  (`deterministicTaskRunId`, `:626-628`). A re-enqueue of the same child gets a
  `conflict` / `already_exists` and is treated as success (`:296-302`,
  `isConflictError` at `:442-451`).
- Completion carries `completionId: "complete-" + taskRunId` (`:417`), so
  replayed completions are idempotent.
- A *pre-execution* request failure releases the task claim rather than
  stranding it until the lock TTL expires (`safeRelease`, `:304-305`).

## Child work is a TaskRun

`enqueueChildTask` (`:275-316`) calls `loom.taskRuns.request(...)`, which the
SDK sends as the `exec-task` op with `enqueueOnly: true`
(`sdk/driver.js:308`), handled by `execTask`
(`internal/webui/handlers/driverapi/module.go:592-597` →
`driver.EnqueueTaskRunWithResult`, `internal/driver/task_request.go:236`). The
request carries `taskId`, the deterministic `taskRunId`, `runner`
(default `local-task-runner`), `parentSessionId`, optional `nodeId`,
`repoRef` from the task's `sourceRepo`, a `workerProfileId`, and a `childInput`
bag (`childTaskInput`, `:346-368`) that forwards repo/branch/PR/stack-lineage
options down to the runner.

`parentSessionId` (`:87`, `:281`) is the lead's orchestration session id — the
attribution channel that replaced the removed `Agent.OrchestratorSessionID`
column (tombstone: `internal/domain/agent.go:53-55`).

Queued runs are executed by serve's task-worker pool
(`driver.TaskWorker`, `internal/driver/task_worker.go:18`; started at
`internal/cli/serve/serve.go:319,349` with concurrency from
`LOOM_DRIVER_TASK_WORKER_CONCURRENCY`, default 2, clamped to 32 —
`serve.go:462`). The pool runs only when the driver executor is enabled
(`serve.go:305`). Queue/lease mechanics live in
[taskrun-queue-and-worker-pool.md](taskrun-queue-and-worker-pool.md).

## What the workflow does *not* do

Three responsibilities were deliberately moved server-side, and the
corresponding workflow-side loops were deleted:

- **Assignment / task-completion notification to the lead.** The workflow calls
  `loom.agents.deliverAssignment` exactly once (`attemptLeadDelivery`,
  `:571-588`). The server attempts one inline delivery and durably enqueues an
  outbox row for anything short of `delivered`/`unsupported`
  (`internal/webui/handlers/driverapi/module.go:456-480`); retry is the outbox
  dispatcher's job (`internal/driver/outbox_dispatcher.go:134`). Task-completion
  messages are created server-side on the terminal task-run transition
  (`createLeadTaskOutbox`, `internal/driver/task_events.go:115-141`). See
  [lead-runtime-delivery.md](lead-runtime-delivery.md).
- **Stale-run recovery.** Owned by the server-side sweeper, not by a workflow
  timer (`epic-runner.ts:46-48`).
- **Retry of a failed child.** The server scheduler retries and, on exhaustion,
  blocks the underlying issue; the workflow only records the outcome
  (`:170-183`).

## Stacked PRs

`--stacked-pull-requests` projects the epic's `blocks` DAG into the per-user
stack store *before* queueing, so the payload can carry lineage to sandboxed
runners that cannot read the host stack store
(`internal/cli/epic/run.go:141-154`, payload key `stackLineage` at `:264-266`,
consumed by `stackLineageDefaults` / `lineageForTask`,
`epic-runner.ts:370-404`). After a successful drain the CLI reconciles the
stack into stacked PRs, fail-open — branches are already pushed, so a
reconcile error is a warning and `loom stack publish` re-runs it
(`internal/cli/epic/run.go:180-188`).

## What `internal/epicrunner` still is

235 non-test lines across `start.go` and `assignment_context.go`. Six exported
functions; the three that matter here are shared with the workflow via the
driver API (the fourth, `ErrorKindOf` at `start.go:60`, only classifies errors):

- `IsLeadRole` (`start.go:71-79`) — the role-name allowlist.
- `AcquireBindLock` / `AcquireBindLockWithTimeout` (`start.go:82,87`) — the
  `~/.loom/epic-runner-locks` file lock serializing ownership changes;
  30s timeout, 100ms poll (`start.go:20-22`).
- `LoadLeadAssignmentContext` / `FormatLeadAssignmentContext`
  (`assignment_context.go:26,65`) — the provider-neutral assignment view fed to
  lead runtimes and Claude hooks. Note `AssignmentVersion` is derived, not
  incremented: `lead.UpdatedAt` formatted RFC3339Nano, falling back to the epic
  id (`assignment_context.go:49-52`).

## Related

- [docs/product/lead-agent-epic-runner-spec.md](../product/lead-agent-epic-runner-spec.md)
  — canonical for the lead↔epic product rules this implements.
- [lead-runtime-delivery.md](lead-runtime-delivery.md) — how the assignment and
  task-completion messages reach the lead's live conversation.
- [taskrun-queue-and-worker-pool.md](taskrun-queue-and-worker-pool.md) — the
  queue, lease and retry mechanics behind each child `TaskRun`.
- [epic-runner-lead-control.md](epic-runner-lead-control.md) — the predecessor
  design and its validation log (historical).
- [native-flue-driver-integration.md](native-flue-driver-integration.md) — how a
  workflow becomes a driver bundle.
- [docs/loom-glossary.md](../loom-glossary.md) — *epic*, *driver run*,
  *task run*, *runner*, *flue*.
