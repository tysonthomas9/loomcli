# FleetDB Agent Platform V2 Execution Topology Addendum

> **Status:** Partially implemented — remediation Steps 1–4 and 6 shipped
> (see "Remediation Plan" below); Steps 5 and 7 are still open.
> *audited 2026-07-23*
> **Date:** 2026-06-05
> **Related:**
> - [`workflow-driver-authoring-guide.md`](workflow-driver-authoring-guide.md) —
>   the current platform contract; read this first for how a driver runs today.
> - [`fleetdb-agent-platform-v2-proposal.md`](fleetdb-agent-platform-v2-proposal.md)
> - [`fleetdb-agent-platform-v2-phased-delivery.md`](fleetdb-agent-platform-v2-phased-delivery.md)
> - [`taskrun-queue-and-worker-pool.md`](taskrun-queue-and-worker-pool.md)
> - [`driver-op-http-api.md`](driver-op-http-api.md)
> - [`flue-daytona-fleetdb-v1-proposal.md`](flue-daytona-fleetdb-v1-proposal.md),
>   [`flue-daytona-runtime-proposal.md`](flue-daytona-runtime-proposal.md), and
>   [`../product/loom-typescript-sdk-spec.md`](../product/loom-typescript-sdk-spec.md)
>   — the V1 predecessors. All three are pointer stubs: the full text lives only
>   on the unmerged `origin/flue-runtime` branch.

## Purpose

This addendum closes an ambiguity in the V2 proposal: the documents describe
local execution, server-side execution, cloud workers, and Daytona sandboxes at
different maturity levels. Reviewers need a single concrete plan that separates:

- what is implemented today;
- what must be fixed before local epic runs are trustworthy;
- what must change before cloud scale-out is safe;
- how Daytona changes the meaning of "worker node."

The core rule does not change: FleetDB remains authoritative for task claim,
task completion, and dependency unlocks. This addendum is about run lifecycle,
executor placement, child-run visibility, recovery, and cloud runtime placement.

## Implementation Facts (as of 2026-06-05, corrected 2026-07-23)

### DriverRun Creation

The run-creation route records a durable `DriverRun` pinned to the current
active `DriverVersion`. It does not execute the driver synchronously. The UI
must treat this as recorded work awaiting an executor.

Correction (2026-07-23): the route is
`POST /api/workspaces/{ws}/workflows/{name}`, not `POST /epics/{epic_id}/runs`
(`internal/webui/handlers/workflows/module.go:40`; the frontend calls it at
`internal/webui/frontend/src/api/workflows/workflows.ts:44`). `/epics/{id}/runs`
survives only as a fleet-db *client* call behind `DriverRunStore.CreateEpic`
(`internal/infra/fleetdb/platform.go:283`, interface at
`internal/store/platform_store.go:509`), which has no caller outside the store
implementations and the CLI tracing wrapper. Every occurrence of
`POST /epics/{epic_id}/runs` in this document should be read as the workflows
route.

Correction (2026-07-23): runs are created `queued`, not `running`
(`internal/infra/memstore/platform_driver_run.go:84`), and `Claim` refuses
anything that is not `queued` (`:204`). The "running forever" defect this
section described is fixed.

This endpoint is still not a true registered product endpoint. The caller
supplies a driver/workflow name and the handler binds the current active
`DriverVersion` at request time. A true registered endpoint must persist the
pinned version, auth policy, idempotency scope, and concurrency policy as
registration data before users invoke it.

Admission is still scan-then-create in the memstore path
(`internal/infra/memstore/platform_driver_run.go:63-69` scans for an active epic
run before creating). That is acceptable for a local proof, but cloud mode needs
store-level constraints or transactions for:

- idempotency key uniqueness within its declared scope;
- one active run per epic — enforced, though by scan, not by constraint;
- workspace/profile capacity reservation — still nothing reserves or releases
  capacity;
- endpoint registration pinning — still unbuilt.

### Driver Executor

The driver executor is not a standalone binary today. It is a background loop
inside `loom serve`.

Correction (2026-07-23): it is **on by default**, not opt-in.
`driverExecutorEnabled` returns true unless `LOOM_DRIVER_EXECUTOR` is `0`,
`false`, `off`, or `no` (`internal/cli/serve/serve.go:453-459`); serve's own help
text says "DriverRun executor toggle (default: on; set 0/false/off/no to
disable)" (`internal/cli/serve/serve.go:126`). The
[authoring guide](workflow-driver-authoring-guide.md) already states the current
behavior. Sentences elsewhere in this document about confusion "when
`LOOM_DRIVER_EXECUTOR` is disabled" describe the opt-in era and no longer apply
to a default install.

The executor scans workspaces, finds recorded runs, claims them, launches the
pinned driver bundle, heartbeats the run claim, and finalizes the run.

In local mode this is correct: one `loom serve` process can serve API/UI and
optionally execute runs for repos checked out on that host.

Executor availability is not modeled as durable placement state today. Runs can
be admitted even when no executor is active, and an executor scans all
workspaces it can see. Before scale-out, a `DriverRun` should record placement
requirements such as workspace, repo/worktree, bundle digest availability,
runtime profile, and required capabilities. Executors should claim only runs
that match their profile.

### Driver Runtime

The driver runtime is the published TypeScript bundle running as a child process
under the executor. (`loom driver run` only records a queued run; it does not
launch a runtime — see Step 1 below.) It uses the driver SDK to ask FleetDB for
ready tasks, claim tasks, complete tasks, or release tasks — over the driver-op
HTTP API, not by spawning CLI subprocesses
([`driver-op-http-api.md`](driver-op-http-api.md)).

The driver runtime is not the privileged coding step. It orchestrates. The
privileged coding step is the `exec-task` path described next.

### `exec-task`

Rewritten 2026-07-23: the name survived, the execution topology did not.

`loom driver exec-task` still exists as a hidden trusted CLI command
(`internal/cli/driver/exec_cmd.go:59-65`), but it is no longer what the SDK
invokes. `ctx.taskRuns.request(...)` POSTs the `exec-task` **driver op** over
HTTP (`sdk/driver.js:308` → `internal/webui/handlers/driverapi/module.go:150`,
route at `:191`) with `enqueueOnly: true`. That records a `queued` `TaskRun` and
returns immediately; a serve-side `TaskWorker` pool claims and executes it
(`internal/cli/serve/serve.go:319`, `:349`). Same op name, completely different
topology — see
[`taskrun-queue-and-worker-pool.md`](taskrun-queue-and-worker-pool.md).

What did not change: the task-execution step deliberately does not claim,
complete, or release FleetDB tasks. The driver script owns those mutations
through the SDK.

The flag is `--provider-profile`, not `--provider`
(`internal/cli/driver/exec_cmd.go:82`), and it is enforced rather than advisory.
`resolveTaskProviderProfile` (`internal/driver/task_scheduling.go:197-238`) maps
`local-noop` to a local sandbox, maps `flue-daytona` to `runner=flue` +
`sandbox=daytona` and errors without a configured host bridge, errors on an
empty profile, and errors on an unknown profile that cannot resolve a sandbox
provider. This closes the open decision and the checklist item at the bottom of
this document.

### Daytona Local Mode

The current Daytona path is host-orchestrated:

```text
local Loom process
  -> creates/controls Daytona sandbox
  -> passes task context and repo hydration data
  -> receives patch/log output
  -> applies patch back to the local worktree
  -> closes or releases the FleetDB task
```

This is a local validation path. It must not be presented as the cloud
production path because it depends on a local worktree as the artifact sink.

## Corrected Lifecycle Model

### DriverRun States

The honest `DriverRun` lifecycle this section asked for shipped
(`internal/domain/platform.go:369-393`):

```text
queued
  -> running
  -> suspended_awaiting_event -> queued        (non-terminal)
  -> completed | failed | needs_review | cancelled
```

| State | Meaning |
|---|---|
| `queued` | The run record exists and is pinned, but no executor has claimed it. |
| `running` | An executor has claimed the run and is heartbeating it. |
| `suspended_awaiting_event` | The run registered an await and released its executor slot; non-terminal (`internal/domain/platform.go:378-381`, `:388-390`). |
| `completed` | The driver exited successfully and finalized the run. |
| `failed` | The driver, bundle verification, executor, or recovery path failed the run. |
| `needs_review` | The driver stopped intentionally and returned control to an operator, reviewer, or lead agent. |
| `cancelled` | An operator or policy stopped the run before normal completion. |

`suspended_awaiting_event` is the one state added since this document was
written, and it was added with defined semantics rather than by overloading an
existing status: `DriverRunStore.Suspend` / `ResumeAwaiting`
(`internal/store/platform_store.go:526`, `:535`) are owner-fenced and keyed by
an await instance (`runID#await-{n}`), with the await-timeout sweeper
(`internal/driver/await_timeout_sweeper.go`) resuming expired awaits through a
synthetic timeout event.

The original rule still holds for anything further: do not add more transient
states until their behavior is defined. If a retry policy lands, introduce
`finalizing` or `stale` with explicit fencing and operator semantics rather than
overloading `failed`. (Cancel did not need a state: it is a request marker,
`DriverRun.CancelRequestedAt`, `internal/domain/platform.go:433`.)

### TaskRun States

`TaskRun` became the visible child attempt record this section asked for.
`domain.TaskRun` (`internal/domain/platform.go:498-539`) carries
`DriverRunID`, `DriverStepID`, `LeaseID`, `FencingToken`, `RunnerPlacement`,
`SandboxPlacement`, token accounting, `logs_ref`/`artifacts_ref`, and retry
state; the worker back-links the parent `DriverStep` after claiming
(`internal/driver/task_worker.go:133`).

The flow shipped with one difference from what this section proposed — the
create and the execute are separated by a queue:

```text
DriverRun running
  -> claim FleetDB task
  -> create TaskRun queued            (workflow, via the exec-task driver op)
  -> serve TaskWorker claims it       (fenced: node + lease + fencing token)
  -> execute in the resolved worktree
  -> finish TaskRun completed | failed
  -> complete or release FleetDB task
```

`ctx.taskRuns.request(...)` therefore returns a real `taskRunId` for a run that
has not executed yet. The workflow learns the outcome from the epic watch stream
(preferred) or `taskRuns.await` polling; see
[`taskrun-queue-and-worker-pool.md`](taskrun-queue-and-worker-pool.md). Logs,
usage, patch metadata, sandbox metadata, and exit code attach to the child
record or to artifacts linked from it.

## Remediation Plan

This was the work order. Steps 1–4 and 6 shipped; Steps 5 and 7 are still open.
The shipped steps are recorded below as history plus the implementing citation —
their original deliverable/proof lists were removed on 2026-07-23 because they
read as future work that is no longer future.

### Step 1: Make Run Recording Honest — SHIPPED

`queued` is a real stored `DriverRun` status (`internal/domain/platform.go:372`)
and runs are created in it (`internal/infra/memstore/platform_driver_run.go:84`).
No compatibility window, no derived-from-`running` read model.

One deliverable was dropped rather than delivered: `loom driver run` was never
made a synchronous create-and-execute path. Its short help is "Record a queued
DriverRun for a published driver" (`internal/cli/driver/driver_cmd.go:69`) and
it prints "Execution pending: start a driver executor/runtime to claim queued
runs" (`:203`). Recording and executing stayed separate everywhere, which is the
stronger version of this step's intent.

### Step 2: Make Admission And Run Claim Atomic — SHIPPED

Claim moved into the store contract:
`Claim(ws, runID, nodeID, leaseID)` / `Heartbeat(..., fencingToken)` / `Finish`
(`internal/store/platform_store.go:512-514`), replacing the process-local mutex.
Claim refuses any run that is not `queued`
(`internal/infra/memstore/platform_driver_run.go:204`) and admission rejects a
create while a `queued`-or-`running` run exists for the epic (`:65`). Heartbeat
and finish are owner- and fence-checked, so a replaced executor cannot write.

Stale-run recovery is fail-and-unblock, exactly as this step required, and the
executor runs it before every claim attempt
(`internal/cli/serve/serve.go:333`, `Executor.RecoverStaleOnce`).

Still open from this step: workspace/profile **capacity reservation**. Nothing
reserves or releases capacity on terminal completion.

### Step 3: Add Real Child TaskRun Visibility — SHIPPED, RESHAPED

Child `TaskRun`s are durable records with their own IDs, lease/fencing,
placement, token accounting, logs/artifacts refs and retry state
(`internal/domain/platform.go:498-539`), driven by
`internal/driver/task_request.go` and `internal/driver/task_worker.go`.
`providerProfile` is mapped or rejected, never silently treated as placement
(`internal/driver/task_scheduling.go:197-238`).

Reshaped: this step assumed `ctx.taskRuns.request(...)` would create *and*
finalize the child in one call, shelling out to `loom driver exec-task`. What
shipped splits those — `request()` enqueues, a serve-side worker pool executes.
See [`taskrun-queue-and-worker-pool.md`](taskrun-queue-and-worker-pool.md).

The paragraph this step wrote about cloud `TaskRun` semantics being stricter
than the local SDK bridge is still the governing rule, and it is now partly
enforced: run-scoped tokens exist (`internal/driver/run.go:253`) and non-local
completions reject `file://`-class artifact URIs — a `TaskRun` whose
`SandboxPlacement.Provider` is anything other than empty/`local`/`local-noop`/
`noop`/`flue-local` must present cloud-safe artifact URIs
(`internal/infra/memstore/platform_task_run.go:545-547`, `:566-583`).

### Step 4: Add Task-Level Stale Recovery — SHIPPED

The chosen mechanism was the Loom-side recovery loop, made automatic rather than
operator-triggered: `internal/driver/stale_task_sweeper.go` plus
`store.RecoverStaleTaskRuns` (`internal/store/platform_store.go:516`), with a
`recover-stale-tasks` driver op for ops use
(`internal/webui/handlers/driverapi/module.go:153`). It fails heartbeat-expired
`TaskRun`s and releases their task claims
(`StaleTaskRunRecoveryResult.Released` / `ReleasedTaskIDs`,
`internal/store/platform_store.go:491-505`).

It is unconditional server policy, not a toggle: it runs "whenever serve has a
store", and the code says so — "Workflows must not call recoverStale themselves"
(`internal/cli/serve/serve_loops.go:25-30`).

fleet-db still has no server-side reaper of its own; the explicit release
endpoints remain real (`internal/backend/fleet/claim_release.go:36`, `:59`).

Note the non-goal was overtaken: automatic retry policy landed anyway, at the
`TaskRun` level with attempt budget and backoff
(`internal/driver/task_retry.go:31`, `:90-99`).

### Step 5: Split Local and Cloud Topologies — OPEN

Local topology remains:

```text
browser
  -> loom serve API/UI
  -> in-process driver executor (default on)
  -> driver runtime child process
  -> exec-task driver op -> queued TaskRun -> serve TaskWorker pool
  -> local backend or host-orchestrated Daytona
  -> local worktree patch-back
  -> FleetDB close/release
```

Cloud topology must be described as roles, not necessarily binaries:

```text
browser/CDN
  -> loom serve API role
  -> FleetDB service
  -> loom serve executor role or later dedicated executor service
  -> runner placement
  -> sandbox placement
  -> server-visible artifacts
  -> FleetDB close/release
```

The same `loom serve` binary can run in two cloud roles:

| Role | Executor | Scaling signal | Responsibility |
|---|---|---|---|
| API role | Off | HTTP request rate | UI, REST API, auth, run recording. |
| Executor role | On | queued run depth and capacity | Claim runs, launch driver runtimes, heartbeat, finalize. |

Extracting a standalone executor binary is optional future cleanup. The current
architecture only requires role separation by configuration.

When these docs say "scheduler" in cloud phases, they mean capacity and
placement scheduling only. FleetDB remains the ready-frontier, claim, and
dependency authority. A Loom scheduler may decide which executor/runtime profile
is allowed to attempt work, but it must not compute dependency readiness or
assign a task outside FleetDB's claim contract.

Capacity must be split into explicit controls:

| Control | Meaning |
|---|---|
| Backlog cap | How many queued runs may be admitted for a workspace or endpoint. |
| Active DriverRun cap | How many claimed/running driver runs may execute concurrently. |
| Executor concurrency cap | How many driver processes one executor instance may launch. |
| Provider cap | How many remote runners or sandboxes a provider profile may hold. |
| Budget cap | Model, token, time, or dollar limit for a workspace/run/profile. |

### Step 6: Separate Runner Placement From Sandbox Placement — SHIPPED

Shipped exactly as prescribed: `domain.TaskRunPlacement`
(`internal/domain/platform.go:541-556`) is one struct instantiated twice on a
`TaskRun`, as `runner_placement` and `sandbox_placement`
(`internal/domain/platform.go:498-539`). No single `RuntimeProvider` field was
overloaded. `resolveTaskProviderProfile` fills both independently, so
`flue-daytona` becomes `runner=flue` + `sandbox=daytona`
(`internal/driver/task_scheduling.go:207-217`).

The conceptual guidance below is unchanged and still governs cloud review.

Cloud review must use two placement concepts:

| Concept | Meaning | Examples |
|---|---|---|
| Runner placement | Where the process that owns lease heartbeats, TaskRun mutation, logs, and artifact upload runs. | local process, executor pod, CI job, Daytona bootstrap process |
| Sandbox placement | Where the code-editing filesystem and shell run. | local worktree, container filesystem, Kubernetes volume, Daytona sandbox |

This avoids the misleading "one worker node hosts N sandboxes" model.
Do not overload a single `RuntimeProvider` field for both ideas. Cloud records
need distinct metadata such as `runner_runtime_provider` and `sandbox_provider`
so a Kubernetes runner controlling a Daytona sandbox is representable.

For on-host containers, the node is a real capacity unit:

```text
executor node
  -> N local containers or microVMs
```

For Daytona, the executor node is only a control loop unless the runner itself
is inside Daytona:

```text
executor/control loop
  -> Daytona API
  -> M provider-side sandboxes
```

In Daytona cloud mode, scale primarily by:

- provider sandbox concurrency;
- scoped runner-token issuance;
- per-workspace and per-provider capacity reservations;
- durable artifact throughput;
- FleetDB claim and completion throughput.

Do not describe cloud Daytona capacity as "N sandboxes inside one node" unless
the deployment actually uses on-host containers.

### Step 7: Make Cloud Artifacts and Credentials First-Class — OPEN

Deliverables before cloud scale-out:

- Move driver bundles to content-addressed object storage or another
  worker-readable immutable store. Still open — `safeBundleRoot` resolves the
  bundle under the work dir with no object-store backend
  (`internal/driver/executor.go:642`). Correction (2026-07-23): the current
  location is not `~/.loom/drivers/<digest>`. Bundles stage under the
  **workspace work dir**: `<workDir>/.loom/drivers/<driverID>/<versionID>`
  (`internal/driver/register.go:413`, `:443`; also documented at
  `internal/driver/task_bridge.go:77`).
- Require server-visible artifacts for cloud `TaskRun`s. DONE for completion
  admission (`internal/infra/memstore/platform_task_run.go:545-547`).
- ~~Reject `file://`, laptop-local, or Daytona-local artifact URIs in cloud
  mode.~~ DONE — enforced at task-run completion for any non-local sandbox
  provider (`internal/infra/memstore/platform_task_run.go:566-583`).
- Mint scoped runner tokens for one `TaskRun` or `DriverRun` action set.
- Enforce fencing so stale attempts cannot write after replacement.
- Persist sandbox metadata before model execution starts.
- In Daytona local mode, verify the worktree base ref before patch apply and
  preserve the patch if apply fails.
- In Daytona cloud mode, fail preflight if the intended base ref is not
  reachable; do not silently fall back to a default branch.
- Define cleanup policy: successful sandbox delete, failed sandbox retention
  with TTL and credential scrub status.

Proof:

- A worker without the publisher's local disk can execute a pinned driver.
- UI can read logs/artifacts after sandbox cleanup.
- A leaked task token cannot mutate unrelated tasks or runs.
- A stale runner receives conflict on late writes.
- A Daytona patch-back conflict leaves inspectable artifacts and does not close
  the FleetDB task.

## Phase Mapping

These are the corrections this addendum applied to the phase definitions in the
phased-delivery doc. For which phases actually shipped, read that document's
"Phase Status" table, not this one.

| Existing phase | Addendum correction |
|---|---|
| Phase 1 | No change: publish and run a local pinned driver. |
| Phase 2 | Add real child `TaskRun` creation around `ctx.taskRuns.request(...)`. |
| Phase 3 | Add honest queued/running lifecycle and task-level stale recovery before claiming full epic-loop reliability. |
| Phase 4 | Treat cloud as API role + executor role + runner placement + sandbox placement. Do not imply a standalone executor binary is implemented. |
| Phase 5 | Distinguish ad hoc epic-run creation from true registered endpoints; both create `queued` runs and execute separately. |
| Phase 6 | Add policy, approvals, capability gates, scoped tokens, stale write fencing, and operator recovery. |

## Acceptance Tests To Add

### Queued Run Lifecycle

- `POST /api/workspaces/{ws}/workflows/{name}` creates `status=queued`.
- With executor disabled, the run remains queued and UI shows executor-disabled
  copy.
- With executor enabled, the run transitions `queued -> running -> completed`.

### Distributed Claim

- Two executor instances race on the same queued run.
- Exactly one claim succeeds.
- The losing executor does not launch the driver bundle.

### Admission Constraints

- Concurrent POSTs for the same epic cannot create two active/queued runs.
- Replaying the same idempotency key returns the original run.
- Capacity reservation is atomic and releases on terminal completion.
- A true registered endpoint invokes its registered `DriverVersion`, not the
  driver's current active version.

### Stale Run Recovery

- Kill an executor while a `DriverRun` is heartbeating.
- After TTL, a surviving executor or recovery command marks the run failed.
- The run is no longer active and the epic can be re-run.

### Child TaskRun Visibility

- A driver claims one task and calls `ctx.taskRuns.request(...)`.
- A child `TaskRun` is created and linked to the parent.
- The child captures status, exit code, logs/artifacts metadata, and runtime
  metadata.

### Task Claim Recovery

- Kill the driver after FleetDB task claim and before close/release.
- Recovery returns the task to a claimable state.
- Dependents remain blocked until FleetDB accepts task completion.

### Daytona Cloud Boundary

- In local Daytona mode, only the host process talks to embedded FleetDB.
- In cloud Daytona mode, the runner uses `LOOM_SERVER_URL` plus scoped tokens.
- Cloud artifacts remain readable after sandbox cleanup.
- Daytona patch-back verifies base ref and preserves patches on conflict.
- Cloud Daytona preflight fails if the requested base ref is not reachable.

## Decisions Resolved Since 2026-06-05

| Decision | Answer | Citation |
|---|---|---|
| Automatic or operator-triggered task stale recovery? | Automatic and always-on; it is server policy, not workflow policy. | `internal/cli/serve/serve_loops.go:25-30` |
| Is `needs_review` a first-class terminal status? | Yes, first-class and terminal. | `internal/domain/platform.go:376`, `:386` |
| Migrate `queued` physically, or derive it? | Physically stored; no compatibility window was needed. | `internal/domain/platform.go:372`, `internal/infra/memstore/platform_driver_run.go:84` |
| First `providerProfile` → backend/runtime/sandbox mapping? | `local-noop` → local sandbox; `flue-daytona` → runner `flue` + sandbox `daytona`; empty and unresolvable profiles error. | `internal/driver/task_scheduling.go:197-238` |

## Open Decisions

- Should stale run recovery fail the run only, or optionally create a new queued
  retry run? (Answered at the `TaskRun` level — retry-then-park,
  `internal/driver/task_retry.go` — but not at the `DriverRun` level.)
- Should the ad hoc workflow-run route remain separate from true registered
  endpoint invocation, or should the ad hoc form be removed after registration
  lands?
- In cloud Phase 4, is the first remote runner placement an executor pod that
  controls Daytona by API, or a bootstrap runner inside Daytona?

## Review Checklist

Before claiming cloud readiness, the implementation must satisfy the following.
Items marked SATISFIED were verified against source on 2026-07-23.

- SATISFIED — Run button creates a queued run, not a fake-running run
  (`internal/infra/memstore/platform_driver_run.go:84`).
- Executor placement is documented as same binary, separate role.
- SATISFIED — Driver runtime, exec-task, runner placement, and sandbox placement
  are named separately (`internal/domain/platform.go:541-556`).
- SATISFIED — Multi-node run claim is store-atomic
  (`internal/store/platform_store.go:512-514`).
- Endpoint admission and idempotency are store-atomic. (One-active-per-epic is
  enforced by scan, not by a store constraint.)
- SATISFIED — Child TaskRuns are real records, not inferred from task ids
  (`internal/domain/platform.go:498-539`).
- SATISFIED — Task-level stale recovery is defined and tested
  (`internal/driver/stale_task_sweeper.go`,
  `internal/driver/stale_task_sweeper_test.go`).
- SATISFIED — `providerProfile` is wired to runtime placement or rejected
  (`internal/driver/task_scheduling.go:197-238`).
- Daytona cloud mode does not depend on a developer-local worktree.
- Daytona patch-back verifies base refs and preserves failed patches.
- Cloud artifacts and credentials are server-visible, scoped, and fenced.
  (Partial: run-scoped tokens exist, `internal/driver/run.go:253`; bundle object
  storage does not.)

## Related

- [`workflow-driver-authoring-guide.md`](workflow-driver-authoring-guide.md) —
  the current platform contract.
- [`taskrun-queue-and-worker-pool.md`](taskrun-queue-and-worker-pool.md) — the
  queue that reshaped Step 3.
- [`driver-op-http-api.md`](driver-op-http-api.md) — the runtime control surface.
- [`fleetdb-agent-platform-v2-proposal.md`](fleetdb-agent-platform-v2-proposal.md)
  — the vision this addendum reconciles.
- [`fleetdb-agent-platform-v2-phased-delivery.md`](fleetdb-agent-platform-v2-phased-delivery.md)
  — the phase ledger.
- [`native-flue-driver-integration.md`](native-flue-driver-integration.md) —
  driver registration.
