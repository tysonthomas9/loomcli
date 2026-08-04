# FleetDB Agent Platform V2 Phased Delivery Proposal

> **Status:** Partially implemented — Phases 0–3 and 6 shipped, 5 shipped on a
> different route, 4 partial. See the per-phase table below. *audited 2026-07-23*
> **Date:** 2026-06-03
> **Related:**
> - [`workflow-driver-authoring-guide.md`](workflow-driver-authoring-guide.md) —
>   the current platform contract; read this first.
> - [`fleetdb-agent-platform-v2-proposal.md`](fleetdb-agent-platform-v2-proposal.md)
> - [`fleetdb-agent-platform-v2-execution-topology-addendum.md`](fleetdb-agent-platform-v2-execution-topology-addendum.md)
> - [`taskrun-queue-and-worker-pool.md`](taskrun-queue-and-worker-pool.md)
> - [`driver-op-http-api.md`](driver-op-http-api.md)
> - [`flue-daytona-fleetdb-v1-proposal.md`](flue-daytona-fleetdb-v1-proposal.md),
>   [`flue-daytona-runtime-proposal.md`](flue-daytona-runtime-proposal.md), and
>   [`../product/loom-typescript-sdk-spec.md`](../product/loom-typescript-sdk-spec.md)
>   — the V1 predecessors. All three are pointer stubs: the full text lives only
>   on the unmerged `origin/flue-runtime` branch.

## Phase Status - 2026-07-23

Every phase below was audited against source on 2026-07-23. Two phases shipped
in a shape the phase text does not describe; read the RESHAPED notes before
using this document as a spec.

| Phase | Status | Evidence |
|---|---|---|
| 0 Contract lock | SHIPPED | Loom adds no scheduler — verified in this repo (no Loom-side task scheduler; recovery only, see "Genuinely open"). The fleet-db half (`fleet-db/internal/storage/claim.go`) is UNVERIFIED from this tree: fleet-db is a separate repository. |
| 1 Local dynamic driver MVP | SHIPPED | `loom driver register --flue-dist` (`internal/cli/driver/driver_cmd.go:51-65`, flag at `:102`, `--activate` at `:109`); immutable versions (`internal/driver/register.go:460-478`). |
| 2 One task end-to-end | SHIPPED, RESHAPED | `TaskRun` is durable (`internal/domain/platform.go:498-539`), but `taskRuns.request()` **enqueues** rather than executing (`sdk/driver.js:308`). |
| 3 Epic loop + lead handoff | SHIPPED, RESHAPED | The builtin epic-runner is watch-driven, not a synchronous loop (`internal/workflows/builtin/epic-runner.ts:41-52`). |
| 4 Cloud scale-out | PARTIAL | Run-scoped HS256 tokens shipped (`internal/driver/run.go:253`); bundle object storage did not (`internal/driver/executor.go:642`). |
| 5 Registered endpoints | SHIPPED ON A DIFFERENT ROUTE | `POST /api/workspaces/{ws}/workflows/{name}` and `/versions` (`internal/webui/handlers/workflows/module.go:39-40`), not `POST /epics/{id}/runs`. Idempotency-key reuse (`internal/infra/memstore/platform_driver_run.go:55-62`) and one-active-run-per-epic (`:63-69`) are enforced in the store, not the route. |
| 6 Hardening | SHIPPED BEYOND SPEC | Container sandbox + egress modes (`internal/driver/sandbox/`), driver trust levels (`internal/domain/platform.go:49-62`), connector grants and audit (`internal/connector/dispatch.go:310`). |

### Why the executor never shells out to `flue run`

Carried over from the 2026-06-06 implementation audit that this section
replaced, and re-verified on 2026-07-24. `flue run` rebuilds the project at
execution time and owns only process-local run history. Loom's durable run path
needs the opposite: it verifies an *already registered* artifact and executes
that exact bytes-on-disk. `loom driver register --flue-dist ./dist` records
`dist/server.mjs` in the manifest's `server_ref`
(`internal/driver/register.go:631`, rejected if it is anything else at `:685`),
and at run time `verifyBundleManifest` re-hashes the whole bundle tree and
fails on a digest mismatch before the server path is handed to the runner
(`internal/driver/executor.go:611-639`). Shelling out to `flue run` would break
that pin. The real-`flue` CLI path exists only as an opt-in smoke
(`LOOM_REAL_FLUE_TEST=1`, `internal/driver/flue_integration_test.go:26`). The
older `loom driver publish` command, which the 2026-06-06 audit records as
generating hidden Flue projects and adapters, no longer exists — there is no
`publish` subcommand under `internal/cli/driver/`.

### What Phase 6 delivered that this document never asked for

- Webhook HMAC verification (`domain.TriggerBinding.WebhookSecret`,
  `internal/domain/platform.go:234-236`), cron bindings with timezone
  (`:251-254`), and actor-filter loop protection with hop-depth accounting
  (`:241-243`, `:290-292`). Routes at
  `internal/webui/handlers/webhooks/module.go:45-49`.
- Run-scoped bearer auth for the driver-op HTTP API — see
  [`driver-op-http-api.md`](driver-op-http-api.md) and `AGENTS.md` § Driver Runtime Auth.
- `TaskRun` retry-then-park with an attempt budget
  (`internal/driver/task_retry.go`), and the always-on stale-task sweeper
  (`internal/cli/serve/serve_loops.go:25-30`).

### Genuinely open

Three V2 data-model/runtime gaps are still open. This list is not the whole
open set — the validation notes below, and Steps 5 and 7 of the
[topology addendum](fleetdb-agent-platform-v2-execution-topology-addendum.md)
(capacity reservation, endpoint registration pinning, cloud artifact
durability), are also open. The three data-model/runtime gaps:

- **Generic `Lease` resource.** Never built. Leases are per-resource:
  `domain.AgentLease` (`internal/domain/control_plane.go:167`),
  `domain.AgentOwnershipLease` (`:182`), plus inline `LeaseID`/`FencingToken`
  fields on `DriverRun` (`internal/domain/platform.go:407-408`) and `TaskRun`
  (`:513-514`).
- **`ActionLedger`.** Never built in this repo. Only the FK string
  `DriverStep.ActionLedgerID` exists (`internal/domain/platform.go:469`),
  plumbed through memstore and the fleet-db client; there is no `ActionLedger`
  type and no `ActionLedgerStore`. The shipped idempotent-side-effect mechanism
  is `domain.ConnectorCallRecord` (`internal/domain/connector.go:253`), written
  by `internal/connector/dispatch.go` for granted and refused outcomes alike.
- **Bundle object storage.** Driver bundles still stage under the workspace work
  dir (`internal/driver/register.go:413`, `:443`) with no object-store backend
  (`internal/driver/executor.go:642`).

### Validation notes still standing

- The opt-in real local Flue toolchain smoke
  (`LOOM_REAL_FLUE_TEST=1 go test ./internal/driver -run TestRealFlue`,
  `internal/driver/flue_integration_test.go:25`) needs an environment with the
  matching `flue` CLI installed. Default coverage uses deterministic fake Flue
  builders and built-server runner tests.
- `scripts/test-real-flue-epic-runner.sh` needs a built real Flue CLI on `PATH`
  or `LOOM_FLUE_BUILD_CMD_JSON` pointing at a built Flue checkout
  (`scripts/test-real-flue-epic-runner.sh:45-78`). It seeds the `A -> B,C -> D`
  DAG (`:293-296`), registers with `--flue-dist` (`:321`), and runs with
  `LOOM_DRIVER_EXECUTOR=1` (`:328`).
- **fleet-db-side contract, not loomcli behavior:** artifact content uploads
  support a configurable HTTP object backend
  (`FLEETDB_ARTIFACT_CONTENT_BACKEND=http`) that writes content-addressed
  objects with PUT and verifies finalized HTTP(S) artifacts by re-reading and
  hashing. UNVERIFIED from this repo: no loomcli code reads that env var. The
  only place it appears outside this document is the local-mode compose fixture,
  which sets it to `local` (`test/local-mode/docker-compose.yml:49`).
- **Re-scoped:** short-lived per-run token minting is no longer open on the Loom
  side. Loom mints run-scoped HS256 JWTs at claim (`internal/driver/run.go:253`,
  signing key at `:208`, TTL at `:215`), and the task-run claim path creates and
  verifies per-run lease tokens. What remains open is **fleet-db API-key
  minting and revocation for remote worker bootstrap**, which is a different
  problem.
- Cloud readiness still needs: provider-specific runtime adapters beyond the
  preflight mapping, managed cloud worker placement/capacity beyond the one-shot
  `loom driver work-task-run` command
  (`internal/cli/driver/exec_cmd.go:67-72`), native cloud object-store adapters,
  UI/ops surfaces, and phase-level end-to-end acceptance.

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
| 5 | Registered endpoints | A pinned DriverVersion backs a Loom-owned run-creation route (shipped as `POST /api/workspaces/{ws}/workflows/{name}`, not `/epics/{epic_id}/runs`) |
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

- Driver SDK method. **Corrected 2026-07-23:** this was written as synchronous
  ("runs the agent + patch-back and returns a `TaskRunResult`, there is no
  separate `wait`"). Commit `491222e25` inverted it. `request()` enqueues a
  `queued` `TaskRun` and returns; a serve-side worker pool executes it:

  ```ts
  // enqueues; does not execute. sdk/driver.js:308 sends enqueueOnly: true.
  // `runner` is a runner NAME from the pinned driver version's manifest
  // (the builtin epic-runner defaults to "local-task-runner",
  // internal/workflows/builtin/epic-runner.ts:83) — NOT a provider profile.
  const queued = await loom.taskRuns.request({ taskId, runner: "local-task-runner" });
  // then either await the epic watch stream (preferred) or poll:
  const result = await loom.taskRuns.await({ taskRunId: queued.taskRunId });
  ```

  The public request field is `runner`, "the user-authored runner name declared
  by the pinned driver version manifest ... the runtime strategy selector"
  (`sdk/driver.d.ts:83-110`). `providerProfile` never made it into the public
  SDK type; it survives Go-side only
  (`internal/driver/task_request.go:206`, resolved at
  `internal/driver/task_scheduling.go:197-238`). See
  [`taskrun-queue-and-worker-pool.md`](taskrun-queue-and-worker-pool.md).

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
  -> loom.taskRuns.request(task_id)            # enqueues a queued TaskRun
  -> serve TaskWorker claims it, runs agent + patch-back, finishes it
  -> workflow observes the terminal TaskRun (epics.watch / taskRuns.await)
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
- **Corrected 2026-07-23:** `loom.taskRuns.request({ taskId })` does not run the
  task synchronously. It enqueues. The shipped loop is edge-triggered: claim
  ready tasks up to `maxConcurrency`, enqueue each as a `TaskRun`, and consume
  the epic watch stream for terminal `TaskRun` events to top the pipeline back
  up. There is no polling cadence and no per-batch barrier
  (`internal/workflows/builtin/epic-runner.ts:41-52`). The script still calls
  `loom.tasks.complete` or `loom.tasks.release` on the terminal event.
- Parent-child visibility from `DriverRun` to child `TaskRun`s.
- Driver terminal outcomes:
  - `completed`;
  - `failed`;
  - `needs_review`.
- Lead-agent handoff payload containing failed task, child run, logs, artifacts,
  and suggested next action.

### Example Driver Shape

The example that used to sit here was wrong in three ways at once — it imported
from a subpath that does not exist (`@loom/sdk/flue`; the real one is
`@loom/sdk/driver`, `internal/driver/register.go:24`), it passed
`providerProfile` (not a public SDK field), and it treated `request()` as
synchronous. Rather than maintain a second version of a working driver, read the
shipped one:

- `internal/workflows/builtin/epic-runner.ts` — the epic drain loop, watch-driven.
- `internal/workflows/builtin/github-review-agent.ts` — a non-epic driver.

Both begin `import { createLoomDriverClient } from '@loom/sdk/driver';`
(`internal/workflows/builtin/epic-runner.ts:1`). The SDK's exported subpaths are
`.`, `./runner`, `./driver`, and `./runtime-adapters` (`sdk/package.json:6-25`);
there is no `./flue`.

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

Initial target as written in 2026-06-03:

```text
POST /epics/{epic_id}/runs
```

**Corrected 2026-07-23:** that route was never registered in `loom serve`. What
shipped is:

```text
POST /api/workspaces/{ws}/workflows/{name}           # create a run
POST /api/workspaces/{ws}/workflows/{name}/versions  # register a version
```

(`internal/webui/handlers/workflows/module.go:39-40`; the frontend calls the
first at `internal/webui/frontend/src/api/workflows/workflows.ts:44`.)
`/epics/{id}/runs` survives only as a fleet-db client call behind
`DriverRunStore.CreateEpic` (`internal/infra/fleetdb/platform.go:283`), which
has no caller outside the store implementations and the CLI tracing wrapper.
Read the `/epics/{epic_id}/runs` references in the rest of this phase as the
workflows route.

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
- Failure in `B` prevents `D` and returns `needs_review`.
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

## Related

- [`workflow-driver-authoring-guide.md`](workflow-driver-authoring-guide.md) —
  the current platform contract for authors.
- [`fleetdb-agent-platform-v2-proposal.md`](fleetdb-agent-platform-v2-proposal.md)
  — the vision these phases decompose.
- [`fleetdb-agent-platform-v2-execution-topology-addendum.md`](fleetdb-agent-platform-v2-execution-topology-addendum.md)
  — executor placement, recovery, runner vs sandbox placement.
- [`taskrun-queue-and-worker-pool.md`](taskrun-queue-and-worker-pool.md) — what
  reshaped Phases 2 and 3.
- [`driver-op-http-api.md`](driver-op-http-api.md) — the runtime control surface.
- [`native-flue-driver-integration.md`](native-flue-driver-integration.md) —
  Phase 1's registration contract.
