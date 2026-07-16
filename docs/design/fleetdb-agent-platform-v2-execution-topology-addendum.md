# FleetDB Agent Platform V2 Execution Topology Addendum

**Status:** Addendum for review
**Date:** 2026-06-05
**Related:**
- `docs/design/fleetdb-agent-platform-v2-proposal.md`
- `docs/design/fleetdb-agent-platform-v2-phased-delivery.md`
- `docs/design/flue-daytona-fleetdb-v1-proposal.md`
- `docs/design/flue-daytona-runtime-proposal.md`
- `docs/product/loom-typescript-sdk-spec.md`

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

## Current Implementation Facts

### DriverRun Creation

`POST /epics/{epic_id}/runs` records a durable `DriverRun` pinned to the current
active `DriverVersion`. It does not execute the driver synchronously. The UI
must treat this as recorded work awaiting an executor.

The current implementation stores new registered endpoint runs as `running`,
which is misleading because an unclaimed run may not have started. This is the
source of "running forever" confusion when `LOOM_DRIVER_EXECUTOR` is disabled.

This endpoint is also not a true registered product endpoint yet. Today the
caller supplies a driver name and the handler binds the current active
`DriverVersion` at request time. A true registered endpoint must persist the
pinned version, auth policy, idempotency scope, and concurrency policy as
registration data before users invoke it.

Admission is currently scan-then-create. That is acceptable for a local proof,
but cloud mode needs store-level constraints or transactions for:

- idempotency key uniqueness within its declared scope;
- one active run per epic;
- workspace/profile capacity reservation;
- endpoint registration pinning.

### Driver Executor

The driver executor is not a standalone binary today. It is an optional
background loop inside `loom serve`, enabled only when:

```bash
LOOM_DRIVER_EXECUTOR=1 loom serve
```

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
under the executor or `loom driver run`. It uses the driver SDK to ask FleetDB
for ready tasks, claim tasks, complete tasks, or release tasks.

The driver runtime is not the privileged coding step. It orchestrates. The
privileged coding step is `loom driver exec-task`.

### `loom driver exec-task`

`loom driver exec-task` is a hidden trusted command invoked by
`ctx.taskRuns.request(...)`. It runs one already-claimed task in the resolved
worktree and exits with success or failure. It deliberately does not claim,
complete, or release FleetDB tasks. The driver script owns those mutations
through the SDK.

The `--provider` flag is advisory in the current path. Actual Daytona use is
selected by the backend/runtime configuration, such as the Flue Daytona mode.

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

Introduce an honest `DriverRun` lifecycle:

```text
queued
  -> running
  -> completed | failed | needs_review | cancelled
```

Definitions:

| State | Meaning |
|---|---|
| `queued` | The run record exists and is pinned, but no executor has claimed it. |
| `running` | An executor has claimed the run and is heartbeating it. |
| `completed` | The driver exited successfully and finalized the run. |
| `failed` | The driver, bundle verification, executor, or recovery path failed the run. |
| `needs_review` | The driver stopped intentionally and returned control to an operator, reviewer, or lead agent. |
| `cancelled` | An operator or policy stopped the run before normal completion. |

Implementation note: existing stored `running` records with empty `NodeID` are
effectively `queued`. Migration can be explicit, or the read model can derive
`queued` for `status=running && node_id=""` during a compatibility window.

Do not add more transient states until their behavior is defined. If a cancel
endpoint or retry policy lands, introduce `cancelling`, `finalizing`, or `stale`
with explicit fencing and operator semantics rather than overloading `failed`.

### TaskRun States

`TaskRun` must become the visible child attempt record promised by the phased
proposal. `ctx.taskRuns.request(...)` should create and finalize a child
`TaskRun` around `exec-task`:

```text
DriverRun running
  -> claim FleetDB task
  -> create TaskRun running
  -> run exec-task
  -> finish TaskRun completed | failed
  -> complete or release FleetDB task
```

The return value from `ctx.taskRuns.request(...)` must contain the real
`taskRunId`, not the task id. Logs, usage, patch metadata, sandbox metadata, and
exit code should attach to this child record or artifacts linked from it.

Until that is implemented, the current `ctx.taskRuns.request(...)` behavior
should be described as a local exec attempt, not production FleetDB TaskRun V1:
it shells out to `exec-task` and returns task id, status, and exit code, but does
not yet create the durable child attempt record promised by the proposal.

## Concrete Remediation Plan

### Step 1: Make Run Recording Honest

Deliverables:

- Add `queued` to the `DriverRun` status model.
- Make `POST /epics/{epic_id}/runs` create `queued` runs.
- Update the Epic Runs UI to display queued vs running distinctly.
- Add an executor-disabled state: when no executor is active, the UI says the
  run is recorded but will not execute until an executor or CLI run starts it.
- Keep `loom driver run <driver> --epic <id>` as the direct local path that
  creates and executes a run in one command.

Proof:

- Clicking Run with `LOOM_DRIVER_EXECUTOR` disabled produces a visible `queued`
  run, not a misleading permanently running run.
- Starting `loom serve` with `LOOM_DRIVER_EXECUTOR=1` claims the queued run and
  moves it to `running`.

Non-goals:

- Cloud workers.
- Daytona bootstrap runners.
- Automatic retries.

### Step 2: Make Admission And Run Claim Atomic

Deliverables:

- Replace scan-then-create admission checks with store-level constraints or
  transactions for idempotency, one-active-run-per-epic, and capacity
  reservation.
- Replace the process-local `ClaimRun` mutex with a store-level conditional
  update:

  ```text
  claim run where run_id = ? and status = queued and node_id = ""
  set status = running, node_id = ?, last_heartbeat = now
  ```

- Give every executor instance a stable unique `NodeID`, lease id, and fencing
  token.
- Make executor heartbeat and finish owner-checked and fence-checked. A stale
  executor must not heartbeat or finalize a run after replacement.
- Keep the stale-run reaper, but document its behavior as fail-and-unblock, not
  resume.
- Add tests for two executors racing to claim the same run using independent
  store/client instances.

Proof:

- Two executor processes cannot both run the same `DriverRun`.
- Concurrent POSTs cannot admit duplicate active runs for one epic.
- Capacity is reserved once and released on terminal run completion.
- A crashed executor's claimed run becomes terminal after heartbeat expiry.
- A run is never reaped while its owning executor is heartbeating.

Non-goals:

- Transparent resume of a partially executed driver.
- Task-level stale-claim recovery. That is Step 4.

### Step 3: Add Real Child TaskRun Visibility

Deliverables:

- Add server/SDK APIs needed for a driver to create and finish child `TaskRun`s,
  or route `ctx.taskRuns.request(...)` through a Loom API that does this
  atomically.
- Change `ctx.taskRuns.request(...)` so it:
  - creates a `TaskRun`;
  - invokes `loom driver exec-task` with the `TaskRun` id in environment;
  - maps `providerProfile` to an explicit backend/runtime configuration, or
    rejects unsupported profiles instead of treating them as placement;
  - records exit code, logs, usage, runtime metadata, and artifacts;
  - finalizes the child record;
  - returns `{ id: taskRunId, taskId, status, exitCode }`.
- Keep FleetDB task completion separate: the driver script still calls
  `ctx.tasks.complete(...)` only after the child attempt succeeds.

Production cloud `TaskRun` semantics are stricter than the current local SDK
bridge. Cloud mode must use scoped run/lease/fence tokens, durable artifact
registration, and server-side `CompleteRun` acceptance. Direct issue
claim/close from a driver script is a compatibility path for local phases, not
the final cloud V1 boundary.

Proof:

- The Epic Runs view shows a parent `DriverRun` with child attempts.
- A failed child attempt is visible even if the parent returns `needs_review`.
- Completing a child `TaskRun` alone does not unlock dependencies; only FleetDB
  task completion does.

Non-goals:

- Cloud artifact durability.
- Remote sandbox bootstrap.

### Step 4: Add Task-Level Stale Recovery

Problem:

Run-level stale reaping is not enough. If a driver crashes after claiming a task
but before releasing or completing it, FleetDB may let the claim lock expire
while the issue remains `in_progress` and absent from the ready frontier.
Current FleetDB claim behavior around expired locks and `in_progress` takeover
must be tested directly; the cloud contract cannot rely on inferred ready-query
behavior.

Deliverables:

- Choose one explicit recovery mechanism before cloud scale-out:
  - FleetDB server-side reaper that returns expired `in_progress` claims to the
    ready frontier; or
  - Loom operator recovery endpoint that releases stale task claims for a run;
    or
  - both, with server-side policy controlling automatic vs manual recovery.
- Record stale-recovery events in the task/run timeline.
- Ensure recovery does not complete or unlock a task without accepted artifacts
  and explicit FleetDB close.

Proof:

- Kill the driver after task claim and before completion.
- After recovery, the task is claimable again and dependents remain blocked.
- No duplicate completion is accepted from the stale attempt.

Non-goals:

- Automatic retry policy. Recovery returns the task to a safe state; retry
  policy can be added later.

### Step 5: Split Local and Cloud Topologies

Local topology remains:

```text
browser
  -> loom serve API/UI
  -> optional in-process driver executor
  -> driver runtime child process
  -> exec-task
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

### Step 6: Separate Runner Placement From Sandbox Placement

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

### Step 7: Make Cloud Artifacts and Credentials First-Class

Deliverables before cloud scale-out:

- Move driver bundles from host-local `~/.loom/drivers/<digest>` to
  content-addressed object storage or another worker-readable immutable store.
- Require server-visible artifacts for cloud `TaskRun`s.
- Reject `file://`, laptop-local, or Daytona-local artifact URIs in cloud mode.
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

- `POST /epics/{epic_id}/runs` creates `status=queued`.
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

## Open Decisions

- Should stale run recovery fail the run only, or optionally create a new queued
  retry run?
- Should task stale recovery be automatic by default or operator-triggered?
- Should `needs_review` be a first-class terminal status now or deferred until
  lead-agent handoff is implemented?
- Should `queued` be physically migrated into storage immediately or derived
  from existing `running && node_id=""` records during rollout?
- Should ad hoc `POST /epics/{id}/runs` remain separate from true registered
  endpoint invocation, or should the ad hoc form be removed after registration
  lands?
- In cloud Phase 4, is the first remote runner placement an executor pod that
  controls Daytona by API, or a bootstrap runner inside Daytona?
- What is the first production-safe mapping from `providerProfile` to concrete
  backend/runtime/sandbox configuration?

## Review Checklist

Before claiming cloud readiness, the implementation must satisfy:

- Run button creates a queued run, not a fake-running run.
- Executor placement is documented as same binary, separate role.
- Driver runtime, exec-task, runner placement, and sandbox placement are named
  separately.
- Multi-node run claim is store-atomic.
- Endpoint admission and idempotency are store-atomic.
- Child TaskRuns are real records, not inferred from task ids.
- Task-level stale recovery is defined and tested.
- `providerProfile` is either wired to runtime placement or rejected.
- Daytona cloud mode does not depend on a developer-local worktree.
- Daytona patch-back verifies base refs and preserves failed patches.
- Cloud artifacts and credentials are server-visible, scoped, and fenced.
