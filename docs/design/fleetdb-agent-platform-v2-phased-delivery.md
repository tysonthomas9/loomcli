# FleetDB Agent Platform V2 Phased Delivery Proposal

**Status:** Phased proposal for review
**Date:** 2026-06-03
**Related:**
- `docs/design/fleetdb-agent-platform-v2-proposal.md`
- `docs/design/fleetdb-agent-platform-v2-execution-topology-addendum.md`
- `docs/design/flue-daytona-fleetdb-v1-proposal.md`
- `docs/design/flue-daytona-runtime-proposal.md`
- `docs/product/loom-typescript-sdk-spec.md`

## Implementation Audit - 2026-06-06

The current implementation is not yet the full V2 contract. Recent audit work
closed the critical early-platform gaps:

- FleetDB `CompleteTaskRun(close_task=true)` now writes the same `issue.close`
  event shape used by manual issue close, and replaying the same completion does
  not duplicate the event.
- Loom now registers already-built native Flue artifacts with
  `loom driver register --flue-dist ./dist`, records `dist/server.mjs` in
  `server_ref`, and the default Node runner executes that built artifact
  through a local IPC launcher. The removed `loom driver publish` path no longer
  generates hidden Flue projects or adapters. An opt-in real Flue CLI smoke
  exists for installed toolchains:
  `LOOM_REAL_FLUE_TEST=1 go test ./internal/driver -run TestRealFlue`.
  The default executor intentionally does not call `flue run`, because
  `flue run` rebuilds at execution time and owns only process-local run history;
  Loom's durable run path verifies and executes the already-registered
  `server_ref` artifact.
- FleetDB `TaskRun` now records `worker_profile_id`, `runner_placement`, and
  `sandbox_placement` as durable fields; `WorkerProfile` records
  `runtime_policy` and `max_parallel`.
- FleetDB exposes an atomic queued `TaskRun` claim API with worker-profile
  eligibility, node drain/expiry checks, node capacity enforcement,
  `WorkerProfile.max_parallel` enforcement, runner placement, and sandbox
  placement. Redis, Postgres, HTTP API, client, and OpenAPI surfaces are
  covered.
- Loom child task execution now creates queued task runs, claims them through
  the TaskRun store contract, and executes/heartbeats/finishes with the claimed
  node, lease, fencing token, and placement payload.
- Loom worker runtimes can now claim an already queued FleetDB `TaskRun` and
  execute it through the same host task-runner bridge via hidden
  `loom driver work-task-run`. This gives cloud/remote workers a concrete
  one-shot claim/heartbeat/finish surface without requiring parent DriverRun
  credentials.
- FleetDB exposes stale `DriverRun` recovery that fails heartbeat-expired
  running runs without owner credentials and releases active-epic admission;
  Redis and Postgres storage paths plus HTTP API/client surfaces are covered.
  Loom's driver executor loop invokes that recovery before claiming queued
  runs.
- FleetDB stale `TaskRun` recovery re-checks Redis heartbeats immediately before
  failing a run, which prevents a live heartbeat after the stale scan from being
  overwritten by recovery.
- Loom now preflights `providerProfile` before creating a child `TaskRun`.
  Built-in noop profiles stay local, `flue-daytona` maps to explicit Flue runner
  and Daytona sandbox placement when a host task-runner command is configured,
  and unsupported/local profiles fail before a child attempt is persisted.
- FleetDB now exposes a registered trigger-route ingress path that resolves an
  enabled `TriggerBinding` by `route_key`, records a `TriggerEvent`, queues the
  pinned `DriverRun`, and records a dispatched `TriggerDelivery`. The existing
  epic-run endpoint shares this dispatcher.
- Cloud/non-local `TaskRun` completion now rejects required artifacts with local
  `file://`, `local:`, or Daytona-local URI schemes. Redis, Postgres, and Loom
  memstore paths still allow local artifacts for local/noop runs but require
  server-visible artifact refs for non-local sandbox providers.
- Loom host task-runners can now return finalized artifact descriptors
  (`artifacts`, `artifact_descriptors`, or `artifactDescriptors`) with
  server-visible URI/hash metadata. Loom registers/finalizes those artifacts
  before returning `TaskRunResult.artifactIds`, so the explicit Flue driver SDK
  can pass them through the existing FleetDB artifact gates.
- FleetDB artifact content uploads now support a configurable HTTP object
  backend (`FLEETDB_ARTIFACT_CONTENT_BACKEND=http`) in addition to the local
  file store. The HTTP backend writes content-addressed objects with PUT,
  returns server-visible URI/hash metadata, and verifies finalized HTTP(S)
  artifacts by re-reading and hashing the object.
- Loom driver and host task-runner subprocesses now start from a scoped base
  environment instead of inheriting the full parent process environment. Broad
  FleetDB, model-provider, cloud-provider, GitHub, token, password, secret, and
  git-config credentials are stripped before per-run protocol variables are
  appended.
- Loom driver runtimes now expose an explicit child-CLI FleetDB handoff
  namespace (`LOOM_DRIVER_FLEET_DB_*`). The `@loom/sdk/flue` helper translates
  that namespace back to the standard FleetDB bootstrap variables only for the
  child `loom driver ...` command, so cloud-mode child commands do not depend on
  broad parent environment inheritance.
- Loom driver runtimes now preserve the configured host task-runner command
  (`LOOM_DRIVER_TASK_RUNNER_CMD_JSON` or `LOOM_DRIVER_TASK_RUNNER_CMD`) inside
  the scoped runtime environment. That lets a real Flue-executed driver request
  non-noop child `TaskRun`s through the host bridge instead of silently losing
  the runner command at the sandbox boundary.
- Native Flue driver execution now attaches Flue runner output to the completed
  `DriverRun.output` (`logs_ref`, captured log tail, and event count), while
  child task logs/artifacts attach to the child `TaskRun` records.
- `scripts/test-real-flue-epic-runner.sh` is an opt-in full-stack MVP harness:
  it builds local `loom` and `fleet-db` binaries, starts real Redis and FleetDB,
  seeds an `A -> B,C -> D` epic, registers a real Flue-built driver, runs
  `loom serve` with the driver executor enabled, and asserts all child tasks and
  child `TaskRun`s complete in dependency order.

Known remaining validation:

- Run the opt-in real local Flue toolchain smoke in an environment with the
  matching `flue` CLI and runtime dependencies installed. The default coverage
  intentionally uses deterministic fake Flue builders and built-server runner
  tests.
- Run `scripts/test-real-flue-epic-runner.sh` with a built real Flue CLI
  available on `PATH`, or with `LOOM_FLUE_BUILD_CMD_JSON` pointing at a built
  Flue checkout. The local `../flue` checkout is not sufficient until
  `packages/cli/dist/flue.js` has been generated.
- Full V2 cloud readiness remains open: provider-specific runtime adapters
  beyond the initial preflight mapping, managed cloud worker placement/capacity
  beyond the one-shot queued worker command, webhook
  signature/filter/schedule providers beyond generic route ingress, scoped
  per-run credential minting, native cloud object-store adapters and operational
  object-store configuration, UI/ops surfaces, and phase-level end-to-end
  acceptance still need dedicated implementation and validation.
- Server-side minting of short-lived per-run FleetDB tokens is still open. The
  task-run claim path now creates and verifies per-run lease tokens for claim,
  heartbeat, finish, and completion, but this is not yet FleetDB API-key
  minting/revocation for remote worker bootstrap.

## Purpose

The broader V2 proposal describes the eventual platform shape, but it is too
large to validate as one implementation. This document reframes V2 as a set of
deliverable, provable phases.

Each phase must answer:

- what user or lead-agent workflow becomes possible;
- what code or product surface is delivered;
- what invariant proves the phase works;
- what is intentionally out of scope.

The central simplification is:

> FleetDB already owns task claim and dependency correctness. Loom must not
> rebuild that logic. Loom adds dynamic drivers, runtime execution, child-run
> visibility, trigger invocation, and remote execution around FleetDB's existing
> claim model.

## Core Contract

FleetDB remains authoritative for:

- ready-task discovery;
- single task claim;
- task state transitions;
- dependency unlocks;
- task completion acceptance.

Loom is responsible for:

- storing dynamic driver source and built versions;
- running a driver as a durable `DriverRun`;
- calling FleetDB-backed claim APIs through the Loom SDK;
- starting child `TaskRun`s for claimed tasks;
- connecting Flue, Daytona, local runners, CI, or cloud workers to those runs;
- preserving logs, artifacts, patch-back results, and lead-agent handoff state.

The early implementation should not introduce a new scheduler, ready frontier,
or task concurrency system unless current FleetDB APIs cannot express the
required operation.

## Minimal Early Model

Early V2 should start with four core records:

```text
Driver
  dynamic TypeScript source owned by a user, team, lead agent, or system

DriverVersion
  immutable built version of a Driver

DriverRun
  one execution of a DriverVersion

TaskRun
  existing finite coding attempt for a claimed FleetDB task
```

See `docs/design/fleetdb-agent-platform-v2-execution-topology-addendum.md` for
the concrete remediation plan that reconciles the current local executor,
registered run creation, child `TaskRun` visibility, recovery semantics, and
cloud runtime placement.

Later phases can add richer bindings, provider records, capability grants, and
operator views only after the core flow is proven.

## Phase Overview

| Phase | Deliverable | Proof |
|---|---|---|
| 0 | Contract reset | FleetDB task claim remains the only task ownership primitive |
| 1 | Local dynamic driver MVP | Native Flue TypeScript project builds, registers, and runs as a `DriverRun` |
| 2 | One task end-to-end | Driver claims one FleetDB task and completes it through Flue + Daytona patch-back |
| 3 | Epic loop with lead handoff | Driver drains an epic through FleetDB dependencies, or returns control to lead |
| 4 | Cloud scale-out | Cloud Loom/FleetDB provisions many Flue-Daytona task runs safely |
| 5 | Registered endpoints | A pinned DriverVersion backs `POST /epics/{epic_id}/runs` |
| 6 | Hardening | Capabilities, approvals, operator recovery, and policy controls |

## Phase 0: Lock The Contract

### Goal

Remove unnecessary architecture from V2 and make the existing FleetDB claim
contract the center of the design.

### Deliverables

- Document that FleetDB owns ready-task claim and dependency unlock.
- Define the minimum Loom records needed around that contract:
  `Driver`, `DriverVersion`, `DriverRun`, and links to `TaskRun`.
- Define the SDK rule: driver code can request a ready task, but FleetDB decides
  whether a task is returned or already claimed.
- Identify current FleetDB behavior for a claimed task whose runner dies.

### Proof

The team can answer these questions without introducing a new scheduler:

- What happens if two drivers ask for the same ready task?
- What happens if the task has already been claimed?
- What FleetDB state prevents dependent tasks from unlocking early?
- What recovery path exists for a claimed task that never completes?

### Non-Goals

- Generic trigger provider framework.
- Cloud scale.
- Endpoint registry.
- New ready-frontier or scheduling tables.

### Exit Criteria

- The V2 docs explicitly say Loom does not reimplement task claim.
- The first implementation task is scoped to driver execution, not task
  scheduling.
- Any missing FleetDB primitive is listed as a narrow dependency, not replaced
  by Loom-side orchestration.

## Phase 1: Local Dynamic Driver MVP

### Goal

Prove that a normal Flue-authored TypeScript project can become the driver for
a Loom run.

### Deliverables

- A supported source layout:

  ```text
  workflows/epic-runner.ts
  package.json
  ```

- A Flue build step owned by the user/agent project:

  ```bash
  flue build --target node --root . --output ./dist
  ```

- A `Driver` draft record.
- A built immutable `DriverVersion` with source digest, bundle digest, build
  diagnostics, and manifest.
- A manual local invocation that creates a `DriverRun`.
- A read-only Loom SDK surface that lets the driver inspect epic/task state.

### Example Command

```bash
loom driver register --flue-dist ./dist --name epic-runner --activate
loom driver run epic-runner --epic <epic_id>
```

### Proof

- FleetDB records one `DriverRun`.
- The run executes the pinned `DriverVersion`, not whatever source is currently
  on disk.
- The driver can read the epic and identify the next ready task.
- No task is claimed or mutated yet.

### Non-Goals

- Running a coding task.
- Daytona.
- Patch-back.
- Registered HTTP endpoints.
- Lead-agent approval flow.

### Exit Criteria

- Invalid TypeScript fails in the Flue build before registration can activate a
  version.
- Source changes after registration do not affect an existing version.
- The driver run output is visible from CLI and persisted in FleetDB-backed
  metadata.

## Phase 2: One Task End-To-End With Patch-Back

### Goal

Prove that a dynamic driver can claim one FleetDB task and complete it through
the existing task-completion path.

### Deliverables

- Driver SDK method:

  ```ts
  loom.tasks.claimReady({ epicId })
  ```

  This delegates to FleetDB's existing claim behavior.

- Driver SDK method (synchronous: it runs the agent + patch-back via the trusted
  `loom` binary and returns a `TaskRunResult` — there is no separate `wait`):

  ```ts
  const result = await loom.taskRuns.request({ taskId, providerProfile: "flue-daytona" });
  ```

- Child `TaskRun` linked to the parent `DriverRun`.
- Flue + Daytona execution for the claimed task.
- Patch artifact returned from the remote sandbox.
- Local trusted Loom process applies patch-back.
- Task completion is submitted through the existing FleetDB completion path.

### Flow

```text
DriverRun
  -> loom.tasks.claimReady(epic_id)
  -> FleetDB returns one claimed task or none
  -> loom.taskRuns.request(task_id, flue-daytona)  # runs agent + patch-back
  -> task agent produces patch/log artifacts, local Loom applies patch-back
  -> loom.tasks.complete(task_id) -> FleetDB accepts completion
  -> dependency graph unlocks through existing FleetDB behavior
```

### Proof

- Two driver runs cannot claim the same task because FleetDB rejects the second
  claim or returns no task.
- The task is not completed until patch-back succeeds locally.
- Patch artifact is preserved if patch-back conflicts.
- Dependent tasks unlock only after FleetDB accepts completion.

### Non-Goals

- Full epic loop.
- Cloud-scale worker pool.
- Registered endpoint invocation.
- New branch/session lifecycle management.

### Exit Criteria

- A single ready task can be claimed, run remotely, patched back locally, and
  completed.
- Duplicate claim attempts are handled by FleetDB, not Loom.
- A remote Daytona sandbox never receives raw FleetDB authority in local mode.
- A failed patch-back leaves the task uncompleted and the patch available for
  inspection.

### Open Check

If current FleetDB has no recovery path for "claimed but runner died," add only
the smallest required recovery mechanism for this phase. Do not introduce a
general scheduler.

## Phase 3: Epic Loop And Lead-Agent Handoff

### Goal

Prove the primary lead-agent workflow:

> The user asks a persistent lead agent to complete an epic end to end. The lead
> agent writes or updates a TypeScript driver. The driver claims and runs tasks
> until FleetDB says no ready work remains, or until a task fails and control
> returns to the lead agent.

### Deliverables

- Driver loop over `loom.tasks.claimReady({ epicId })`.
- `loom.taskRuns.request({ taskId })` runs each task synchronously and returns
  its `TaskRunResult` (status + exit code); the script then calls
  `loom.tasks.complete` or `loom.tasks.release`.
- Parent-child visibility from `DriverRun` to child `TaskRun`s.
- Driver terminal outcomes:
  - `completed`;
  - `failed`;
  - `needs_human`.
- Lead-agent handoff payload containing failed task, child run, logs, artifacts,
  and suggested next action.

### Example Driver Shape

```ts
import { createLoomDriverClient } from "@loom/sdk/flue";

export async function run(ctx) {
  const input = ctx.payload || {};
  const loom = createLoomDriverClient({ input });

  while (true) {
    const task = await loom.tasks.claimReady({ epicId: input.epicId });

    if (!task) {
      return loom.completed({ summary: "Epic drained" });
    }

    // request() runs the agent + patch-back and returns synchronously.
    const result = await loom.taskRuns.request({
      taskId: task.id,
      providerProfile: "flue-daytona",
    });

    if (result.status === "completed") {
      await loom.tasks.complete(task.id); // FleetDB unlocks dependents
    } else {
      await loom.tasks.release(task.id);
      return loom.needsHuman({
        summary: `Task ${task.id} failed`,
        taskRunId: result.id,
      });
    }
  }
}
```

### Proof

- DAG example `A -> B,C -> D` completes in dependency order.
- The driver does not compute dependency unlocks itself.
- If `B` fails, `D` does not run.
- The lead agent receives enough context to explain the failure to the user.

### Non-Goals

- Running task agents inside the lead agent sandbox.
- Re-provisioning lead sandboxes per interaction.
- Branch/session lifecycle management for child agents inside the lead sandbox.
- Generic Slack/GitHub/webhook triggers.

### Exit Criteria

- A persistent lead agent can initiate an epic run.
- The run drains all available work allowed by FleetDB.
- Partial progress is visible.
- Failure returns control to the lead agent without losing child-run context.

## Phase 4: Cloud FleetDB And Flue-Daytona Scale-Out

### Goal

Prove that the same FleetDB-owned claim model works when Loom runs against cloud
FleetDB and provisions many Flue-Daytona task runs.

### Deliverables

- Cloud Loom server connected to cloud FleetDB.
- Worker pool capable of running Flue-Daytona task agents.
- Runner token model scoped to one task/run.
- Log and artifact upload path from remote workers.
- Sandbox cleanup after artifacts are durable.
- Basic capacity controls: max concurrent runs per workspace and per worker
  profile.

### Proof

- Run 100 tasks across multiple epics.
- No duplicate task claims.
- Failed worker does not complete or unlock the task.
- Artifacts remain available after sandbox cleanup.
- FleetDB dependency behavior is unchanged from local mode.

### Non-Goals

- Full marketplace of runtime providers.
- Sophisticated fairness scheduler.
- Multi-region worker placement.
- Registered external webhooks.

### Exit Criteria

- Cloud and local runs use the same task claim and completion contract.
- Remote workers never receive broad FleetDB credentials.
- Operators can see active, failed, and completed task runs.
- Orphan sandboxes can be identified and cleaned up manually.

## Phase 5: Registered Driver Endpoints

### Goal

Allow a pinned dynamic driver to become an invocable product surface.

Initial target:

```text
POST /epics/{epic_id}/runs
```

### Deliverables

- Register a `DriverVersion` behind the endpoint.
- Manual or UI request creates a `DriverRun`.
- Idempotency applies to `DriverRun` creation.
- One active `DriverRun` per epic is enforced.
- Updating driver source does not affect the pinned endpoint until a new
  version is registered.

### Proof

- Duplicate POST with the same idempotency key returns the same `DriverRun`.
- A second POST with a different idempotency key while an epic run is active
  returns the active run or a deterministic conflict.
- Endpoint runs still rely on FleetDB claim for task ownership.
- Pinned `DriverVersion` behavior is stable across source edits.

### Non-Goals

- Slack.
- GitHub webhooks.
- Message bus triggers.
- Public customer endpoint marketplace.

### Exit Criteria

- The UI or API can start an epic run through the endpoint.
- DriverRun idempotency and task-claim idempotency are clearly separate.
- Endpoint registration is version-pinned.

## Phase 6: Hardening And Team Platform Controls

### Goal

Make dynamic drivers safe enough for teams, lead agents, and eventually
external triggers.

### Deliverables

- Capability grants for driver operations.
- Driver approval flow for lead-authored code.
- Build sandbox policy.
- Runtime sandbox policy.
- Secret scoping.
- Endpoint conflict checks.
- Operator views for:
  - failed `DriverRun`s;
  - failed child `TaskRun`s;
  - stuck claimed tasks;
  - patch-back conflicts;
  - sandbox cleanup;
  - build and validation failures.

### Proof

- Driver requesting a disallowed capability cannot run or cannot perform the
  denied action.
- Lead-created driver cannot self-approve a privileged endpoint.
- Endpoint registration with weaker auth or overlapping route is rejected.
- Operator can recover or escalate a failed run without inspecting raw database
  state.

### Non-Goals

- Solving every provider integration before HTTP epic runs are proven.
- Replacing FleetDB dependency semantics.
- Building a generalized workflow engine separate from Loom/FleetDB.

### Exit Criteria

- The platform can safely allow lead-authored driver code under policy.
- Failed runs are inspectable and recoverable.
- The team can decide whether to add GitHub, Slack, CI, schedules, or message
  bus triggers as follow-on provider adapters.

## E2E Acceptance Tests

These tests should be added as phases land.

### Phase 1 Tests

- Build a native Flue project and register `./dist` into an immutable
  `DriverVersion`.
- Change source after registration; run still executes the original bundle
  digest.
- Invalid TypeScript fails the Flue build or native registration and cannot be
  invoked.

### Phase 2 Tests

- Driver claims one ready task and starts one child `TaskRun`.
- Duplicate driver attempt cannot claim the same task.
- Remote Daytona task produces a patch artifact.
- Local patch-back conflict preserves the patch and does not close the task.
- Completion unlocks dependents only after FleetDB accepts it.

### Phase 3 Tests

- Epic DAG `A -> B,C -> D` completes in correct order.
- Failure in `B` prevents `D` and returns `needs_human`.
- Lead agent can display the failed task, child run, logs, and artifact refs.

### Phase 4 Tests

- 100 cloud task runs complete without duplicate claims.
- Worker death leaves task uncompleted.
- Logs and artifacts survive sandbox cleanup.
- Remote worker token cannot mutate unrelated tasks.

### Phase 5 Tests

- Duplicate `POST /epics/{epic_id}/runs` with the same idempotency key returns
  the same `DriverRun`.
- Concurrent POSTs for the same epic do not create two active epic runs.
- Endpoint uses pinned `DriverVersion` after source changes.

### Phase 6 Tests

- Capability denial blocks driver action server-side.
- Lead-authored endpoint with weaker auth is rejected.
- Operator can identify and recover a stuck claim or failed patch-back.

## Proposed Implementation Order

1. Update V2 docs to make FleetDB single-claim authority explicit.
2. Implement native Flue artifact registration without Loom-generated layout.
3. Add `Driver`, `DriverVersion`, and `DriverRun` persistence.
4. Add read-only driver SDK for epic/task inspection.
5. Add task claim/start/wait SDK methods that delegate to FleetDB and existing
   `TaskRun` behavior.
6. Wire Flue + Daytona for one child task with local patch-back.
7. Add epic loop and lead-agent handoff.
8. Move the same flow to cloud FleetDB and remote workers.
9. Add registered `POST /epics/{epic_id}/runs`.
10. Harden with policy, approval, capability, and operator recovery controls.

## Summary

This phased approach keeps the first proof small:

```text
dynamic TypeScript driver
  -> FleetDB claim
  -> one TaskRun
  -> Flue + Daytona
  -> patch-back
  -> FleetDB completion
```

Only after that works should Loom expand into full epic loops, cloud scale,
registered endpoints, and broad provider integrations. The guiding rule across
all phases is that FleetDB remains the authority for task ownership and
dependency unlocks.
