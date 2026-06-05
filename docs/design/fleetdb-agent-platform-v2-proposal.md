# FleetDB-Backed Loom Agent Platform V2 Proposal

**Status:** V2 proposal for review
**Date:** 2026-06-03
**Related:**
- `docs/design/fleetdb-agent-platform-v2-phased-delivery.md`
- `docs/design/flue-daytona-fleetdb-v1-proposal.md`
- `docs/design/flue-daytona-runtime-proposal.md`
- `docs/design/distributed-control-plane.md`
- `docs/design/distributed-control-plane-data-model.md`
- `docs/product/daemon-agent-runtime-architecture.md`
- `docs/product/local-mode-product-spec.md`
- `docs/product/orchestrator-worker-model.md`
- `docs/product/session-artifact-contract.md`
- `docs/product/loom-typescript-sdk-spec.md`

## Purpose

V1 focused on the finite TaskRun path for Flue + Daytona:

```text
FleetDB ready task -> TaskRun -> lease -> runner -> sandbox -> artifacts -> CompleteRun
```

That is still required, but it is not enough for Loom's platform goal.
Loom should be a platform for managing and orchestrating coding agents that can
be invoked by humans, long-running lead agents, schedules, GitHub, CI, webhooks,
and custom TypeScript.

The V2 correction is:

> FleetDB is the durable data layer for the agent platform. Loom is the
> orchestration, API, policy, and runtime-control layer over FleetDB. Flue,
> Daytona, local shells, CI, containers, and Kubernetes are replaceable runtime
> providers.

V2 defines the broader platform model needed to support:

- dependency-driven FleetDB epic task runners;
- persistent lead/support/service agents;
- dynamic user-authored or lead-authored TypeScript workflow drivers;
- schedules, GitHub, CI, webhooks, and custom TypeScript triggers;
- local and cloud deployments using the same FleetDB-backed state model;
- push-assisted and pull-authoritative execution;
- result flow back into FleetDB so task dependencies unlock safely.

## Executive Summary

Loom should not build a separate workflow database or treat runtime providers as
sources of truth. Every durable platform fact belongs in FleetDB or in blob
storage referenced by FleetDB metadata.

```text
Custom TS / GitHub / CI / Schedule / Human / Lead Agent
        |
        v
Loom API / Orchestrator / SDK
        |
        v
FleetDB data layer
  - epics, tasks, dependencies
  - driver source, versions, manifests
  - trigger bindings and events
  - driver runs and run steps
  - agent services and worker profiles
  - task runs, leases, nodes
  - sessions, artifacts, action ledger
        |
        v
Runner placement
  local CLI / daemon / CI job / pod / Daytona bootstrap
        |
        v
Sandbox placement
  local filesystem / Flue virtual FS / Daytona / container / Kubernetes volume
```

The main design rule:

> All invocation paths create durable FleetDB-backed intent before work starts,
> and all finite coding work completes through a fenced FleetDB-backed
> `CompleteRun` path.

## What V2 Changes From V1

V1 answered: how does one ready FleetDB task run in Flue + Daytona and report
back safely?

V2 answers: how does Loom become a team platform where agents can be invoked by
many sources while FleetDB remains the durable data layer?

| Area | V1 | V2 |
|---|---|---|
| Primary scope | Flue + Daytona task execution | FleetDB-backed agent platform |
| Durable center | TaskRun | DriverVersion, TriggerBinding, TriggerEvent, DriverRun, AgentService, TaskRun |
| Invocation sources | FleetDB ready frontier | Human, lead, schedule, GitHub, CI, webhook, custom TS, ready frontier |
| Long-running agents | Mentioned as separate | First-class AgentService |
| TypeScript | Runner SDK | Versioned dynamic workflow drivers plus runner SDK |
| Data layer | FleetDB for tasks/runs | FleetDB for platform state |
| Runtime providers | Flue + Daytona | Local, CI, Flue, Daytona, containers, Kubernetes |
| Scale-out | Phase 4 task sandboxes | Platform-level scheduler over FleetDB |

## Non-Negotiable Principles

1. **FleetDB is the system of record.**
   FleetDB owns durable platform state: tasks, dependencies, triggers, events,
   driver runs, task runs, leases, sessions, artifact metadata, and action
   ledger entries.

2. **Loom is the control plane over FleetDB.**
   Loom validates auth, applies policy, dispatches work, manages runtime
   providers, exposes APIs and SDKs, and writes state through FleetDB-backed
   contracts.

3. **Runtimes are adapters.**
   Flue, Daytona, local shells, CI, Kubernetes, and containers execute work.
   They do not own dependency state, run authority, task completion, or durable
   workflow truth.

4. **Pull owns authority; push only reduces latency.**
   Runners acquire work, leases, and commands through FleetDB-backed APIs.
   Push/SSE/websocket signals can wake or cancel, but missing push events must
   only delay progress.

5. **TaskRun is for finite auditable work.**
   Lead agents, support agents, on-call agents, and scheduled bots are
   long-running `AgentService`s or `DriverRun`s. They create `TaskRun`s only
   when finite coding/review/test work is needed.

6. **FleetDB dependency closure unlocks downstream work.**
   A model response, patch, commit, or PR does not unlock dependent tasks by
   itself. The dependency graph advances only when FleetDB accepts the task
   status transition through the completion policy.

7. **Sandboxes do not get raw FleetDB authority.**
   Cloud runners and remote sandboxes use scoped runner tokens or command
   capabilities. They do not receive broad FleetDB credentials.

8. **Artifacts must be durable before completion.**
   FleetDB can store artifact metadata and content hashes. Large blobs may live
   in object storage, but FleetDB remains the index and source of truth.

9. **Dynamic workflow code is a versioned platform artifact.**
   A user or lead agent must be able to create a `.ts` file that becomes the
   driver for an agent run. FleetDB should store the driver source reference,
   built bundle reference, manifest, permissions, version, and activation state.

10. **FleetDB stores stable envelopes, not every workflow variation.**
    Do not add a new FleetDB enum or table for every possible trigger, step, or
    workflow shape. FleetDB needs durable generic envelopes plus indexed fields
    for common queries; the TypeScript driver owns domain-specific branching.

## Platform Block Diagram

```text
                         +-----------------------------+
                         |  External invocation source |
                         |-----------------------------|
                         | Human UI / CLI              |
                         | Lead or support agent       |
                         | Schedule                    |
                         | GitHub webhook              |
                         | CI workflow                 |
                         | Custom TypeScript driver    |
                         | Lead-created .ts driver     |
                         | FleetDB ready frontier      |
                         +--------------+--------------+
                                        |
                                        v
                         +-----------------------------+
                         | Loom API / Orchestrator     |
                         |-----------------------------|
                         | Auth and policy             |
                         | Trigger ingestion           |
                         | Workflow dispatch           |
                         | Runtime provider control    |
                         | SDK facade                  |
                         +--------------+--------------+
                                        |
                                        v
+-----------------------------------------------------------------------+
| FleetDB data layer                                                    |
|-----------------------------------------------------------------------|
| Workspace / repo / team state                                         |
| Epic tasks and dependency graph                                       |
| Driver / DriverVersion / driver manifest                              |
| TriggerBinding / TriggerEvent / TriggerDelivery                       |
| DriverRun / DriverStep                                                |
| AgentService / WorkerProfile                                          |
| TaskRun / Lease / Node / Runner heartbeat                             |
| Session / TerminalSession / transcript metadata                       |
| Artifact metadata / hashes / object refs                              |
| ActionLedger / audit events / idempotency keys                        |
+-------------------------------+---------------------------------------+
                                |
                                v
                 +------------------------------+
                 | Runner placement             |
                 |------------------------------|
                 | local CLI                    |
                 | local daemon child           |
                 | CI runner                    |
                 | container or Kubernetes pod  |
                 | Daytona bootstrap process    |
                 +--------------+---------------+
                                |
                                v
                 +------------------------------+
                 | Sandbox placement            |
                 |------------------------------|
                 | local worktree               |
                 | Flue default virtual FS      |
                 | Flue local()                 |
                 | Daytona sandbox              |
                 | container filesystem         |
                 | Kubernetes volume            |
                 +------------------------------+
```

## Core Data Ownership

| Resource | Owner | Notes |
|---|---|---|
| Workspace, repo, team, settings | FleetDB | Durable product state |
| Epic/task/dependency graph | FleetDB | Source of scheduling truth |
| Ready frontier | FleetDB query/projection | The scheduler consumes this, agents do not invent it |
| Driver definitions | FleetDB | Name, owner, permissions, active version, source policy |
| Driver versions | FleetDB | Source refs, bundle refs, manifests, validation state |
| Trigger bindings | FleetDB | Generic invocation-surface-to-driver bindings |
| Trigger events | FleetDB | Normalized, deduped, replayable event records |
| Driver runs | FleetDB | Durable execution of one DriverVersion from one event/request |
| Agent services | FleetDB | Long-running desired agents and service leases |
| Worker profiles | FleetDB | Reusable finite-task execution policy |
| Task runs | FleetDB | One finite attempt against one task or driver step |
| Leases/fencing | FleetDB | Authority for runner mutations |
| Nodes/runners | FleetDB | Observed runtime capacity and heartbeat state |
| Sessions | FleetDB | Telemetry index, transcript/session metadata |
| Artifact metadata | FleetDB | Type, hash, size, refs, retention, visibility |
| Artifact blobs | Object store or local store | Referenced by FleetDB metadata |
| Action ledger | FleetDB | Idempotent side effects and external writes |
| Local PIDs, sockets, temp dirs | Node-local runtime | Never global truth |
| Sandbox provider IDs | Runtime metadata in FleetDB | Useful for diagnostics, not scheduling truth |

## Core FleetDB Entities

### Driver

User-authored or lead-authored TypeScript program that can drive an agent,
workflow, or external event handler. The driver is the product-level object;
its versions are immutable build artifacts.

```text
Driver
  id
  workspace_key
  name
  owner_type: user | team | lead_agent | system
  owner_ref
  description optional
  active_version_id optional
  visibility: private | workspace | organization
  default_permissions
  default_runtime_policy
  status: draft | active | disabled | archived
  created_at
  updated_at
```

The driver can be created from:

- a committed repo file such as `.loom/workflows/triage.ts`;
- a lead-agent-authored file staged in a persistent service sandbox;
- an uploaded source bundle;
- a generated Flue project containing `.flue/workflows/*.ts`,
  `.flue/agents/*.ts`, connectors, skills, and `app.ts`.

### DriverVersion

Immutable version of a workflow driver.

```text
DriverVersion
  id
  driver_id
  workspace_key
  version
  source_ref
  source_digest
  bundle_ref
  bundle_digest
  build_target: node | cloudflare | runner
  runtime: flue | loom_ts | other
  manifest
  declared_inputs_schema_ref optional
  declared_outputs_schema_ref optional
  declared_capabilities
  dependency_lock_ref optional
  created_by
  created_at
  validation_status: pending | passed | failed
  validation_errors_ref optional
```

FleetDB does not need to understand every branch in the TypeScript. It stores
the source/bundle identity, manifest, declared capabilities, validation result,
and durable run state. The driver code owns domain-specific decisions inside a
capability envelope.

### TriggerBinding

Connects an external or internal invocation surface to a pinned
`DriverVersion`.

```text
TriggerBinding
  id
  workspace_key
  name
  source_kind
  source_ref optional
  source_config_ref optional
  route_key optional
  method optional
  path_template optional
  topic optional
  event_type_patterns
  filter_ref optional
  driver_id
  driver_version_id
  target_entrypoint
  target_agent_service_id optional
  concurrency_policy: allow | forbid | replace | queue
  idempotency_policy
  auth_policy
  permissions
  enabled
  created_at
  updated_at
```

Examples:

- `POST /epics/{epic_id}/runs`;
- nightly repo maintenance schedule;
- GitHub issue opened triage;
- CI failure remediation;
- Slack message handler;
- message bus topic consumer;
- support escalation webhook;
- custom TypeScript workflow entrypoint;
- lead-created workflow driver invoked by a human or another agent.

`source_kind` is a string with reserved built-ins such as `manual`, `schedule`,
`github`, `ci`, `webhook`, and `fleetdb_ready`, but FleetDB should not require a
schema migration for every new provider. Provider-specific config lives behind
`source_config_ref` and is indexed only where the product needs queries.

The binding is the product/API contract. For example, `POST
/epics/{epic_id}/runs` can be a Loom-owned HTTP route that resolves to
`complete-epic@v3`. A Slack event, GitHub webhook, Kafka topic, schedule tick,
or lead command uses the same binding shape.

### TriggerEvent

Immutable record that an event happened.

```text
TriggerEvent
  id
  workspace_key
  trigger_binding_id optional
  source_kind
  source_event_id
  event_type
  subject_ref
  actor_ref
  occurred_at
  received_at
  idempotency_key
  raw_payload_ref
  raw_payload_digest
  signature_status
  replay_of_event_id optional
```

Ingestion always persists a `TriggerEvent` before dispatch. Duplicate events
should resolve to the existing event or a `duplicate` delivery, not launch
another uncontrolled runner.

### TriggerDelivery

Records evaluation and dispatch outcome for an event.

```text
TriggerDelivery
  id
  trigger_event_id
  trigger_binding_id
  status: accepted | rejected | duplicate | queued | dispatched | failed | replayed
  rejection_reason optional
  driver_run_id optional
  attempt
  next_retry_at optional
  error_class optional
  created_at
  updated_at
```

### DriverRun

Durable execution of one `DriverVersion` from one event, request, schedule tick,
message, or lead command.

```text
DriverRun
  id
  workspace_key
  trigger_event_id optional
  requested_by
  source_kind
  source_ref
  driver_id
  driver_version_id
  entrypoint
  status: queued | starting | running | pausing | paused | resuming | cancelling | cancelled | stale | completed | failed
  concurrency_key
  idempotency_key
  lease_id optional
  input_ref
  output_ref optional
  checkpoint_ref optional
  started_at
  ended_at
  summary
```

The driver may create issues, comments, replies, driver steps, agent service
commands, or task runs. `AutomationRun`, `EpicRun`, or `SupportRun` can be
product-specific labels or views over `DriverRun`, but the platform primitive
should be `DriverRun`.

### DriverStep

Auditable step under a driver run.

```text
DriverStep
  id
  driver_run_id
  step_kind
  status: queued | running | waiting | completed | failed | skipped
  task_run_id optional
  action_ledger_id optional
  external_ref optional
  input_ref
  output_ref
  started_at
  ended_at
```

`step_kind` is an open string, not a closed enum. FleetDB may reserve common
values like `run_agent`, `create_task`, `comment`, or `gate` for UI affordances,
but unknown kinds must still be storable, inspectable, retryable, and auditable.

### AgentService

Long-running desired agent process.

```text
AgentService
  id
  workspace_key
  name
  kind: lead | support | triage | on_call | scheduled | maintenance | orchestrator
  desired_state: running | stopped | paused
  role_name
  profile_name optional
  trigger_refs
  placement_policy
  max_instances
  lease_id optional
  restart_policy
  permissions
  budget_policy
  state_ref optional
  created_at
  updated_at
```

An `AgentService` can keep persistent state. For example, a lead agent may have
a persistent Daytona sandbox with files outside the repo that must survive
branch changes. That service state is distinct from finite child `TaskRun`s.

### WorkerProfile

Reusable execution policy for finite task workers.

```text
WorkerProfile
  id
  workspace_key
  name
  role_name
  backend
  runtime_policy
  repo_filters
  task_filters
  max_parallel
  budget_policy
  permissions
```

### TaskRun

One finite execution attempt for one task or driver step.

```text
TaskRun
  id
  workspace_key
  task_id optional
  driver_run_id optional
  driver_step_id optional
  parent_session_id optional
  worker_profile_id optional
  status: queued | leased | starting | running | finalizing | completed | failed | expired | cancelled
  attempt
  lease_id
  runner_placement
  sandbox_placement
  branch
  base_ref
  final_ref optional
  task_version_at_start
  dependency_projection_version_at_start
  created_at
  started_at
  ended_at
  error_class optional
  error_message optional
```

`TaskRun` is the only V2 unit that can close/unblock a FleetDB task through the
completion policy.

### Lease

Generic fenced authority.

```text
Lease
  id
  workspace_key
  resource_type: agent_service | driver_run | task_run | terminal | artifact_upload
  resource_id
  holder_node_id
  holder_runner_id
  token_hash
  fencing_token
  expires_at
  acquired_at
  renewed_at
```

Every mutating runner call must include the current resource ID, lease ID, and
fencing token. Stale writers get rejected.

### Artifact

FleetDB stores the index and validation metadata.

```text
Artifact
  id
  workspace_key
  owner_type: task_run | driver_run | agent_service | session
  owner_id
  type: patch | diff | commit | pr | log | transcript | test_result | usage | screenshot | report
  uri
  content_hash
  size_bytes
  visibility
  redaction_status
  durable_status: declared | uploading | finalized | failed
  created_at
  finalized_at
```

Large artifact contents can live in object storage. FleetDB remains the lookup,
hash, retention, and ownership authority.

### ActionLedger

Idempotent record of side effects.

```text
ActionLedger
  id
  workspace_key
  idempotency_key
  action_type: close_task | comment | create_pr | merge_pr | start_task_run | update_status
  target_ref
  requested_by
  status: pending | applied | failed | skipped
  request_ref
  response_ref
  created_at
  applied_at
```

This prevents duplicate comments, duplicate PRs, duplicate task closes, and
duplicate runner starts when events are retried or replayed.

## Dynamic Workflow Driver Lifecycle

V2 must support this product flow:

```text
user or lead agent writes TypeScript
  -> Loom saves source as Driver draft
  -> Loom builds and validates a DriverVersion
  -> FleetDB stores source ref, bundle ref, manifest, permissions, digest
  -> TriggerBinding or manual invocation targets that driver version
  -> DriverRun executes the driver
  -> driver emits actions, messages, TaskRuns, artifacts, and result data
  -> FleetDB stores the durable envelopes and action ledger
```

The driver source can look Flue-native:

```ts
// .loom/workflows/triage.ts
import { createAgent, type FlueContext } from "@flue/runtime";
import { loom } from "@loom/sdk/flue";
import * as v from "valibot";

export async function run(ctx: FlueContext) {
  const event = loom.event(ctx);
  const triage = createAgent(() => ({
    model: "anthropic/claude-sonnet-4-6",
  }));

  const harness = await ctx.init(triage);
  const session = await harness.session(`issue:${event.subject.ref}`);
  const { data } = await session.prompt("Triage this issue and recommend next action.", {
    result: v.object({
      severity: v.picklist(["low", "medium", "high", "critical"]),
      needsFix: v.boolean(),
      summary: v.string(),
    }),
  });

  if (data.needsFix) {
    await loom.actions.startTaskRun({
      taskSelector: { issueRef: event.subject.ref },
      profile: "coder",
      idempotencyKey: event.key("fix"),
    });
  }

  return data;
}
```

The important product rule is that this TypeScript is the flexible driver, not
FleetDB. FleetDB records:

- who authored the driver;
- which source and dependency lock were built;
- which bundle digest was executed;
- what capabilities the driver requested and was granted;
- which event invoked it;
- which actions it emitted;
- which TaskRuns, sessions, and artifacts resulted.

FleetDB should not attempt to model every branch in the driver as relational
schema. It only needs stable control-plane envelopes, auditability, leases,
idempotency, and queryable summaries.

### Lead-Created Drivers

A persistent lead agent may create or edit a `.ts` driver as part of its work.
That should be a first-class platform workflow:

```text
lead agent writes /workspace/.loom/workflows/fix-ci.ts
  -> lead requests "publish driver"
  -> Loom snapshots source and dependency metadata
  -> build runs in an isolated build sandbox
  -> validation checks permissions, imports, manifest, and static policy
  -> human or policy gate activates DriverVersion
  -> lead or user registers TriggerBindings for future invocation
  -> binding/manual invocation can run it as a DriverRun
```

Lead-authored drivers should default to `draft`. Activation can require human
approval, code review, tests, or a workspace policy because publishing a driver
is equivalent to granting executable code authority.

### Driver Build And Execution Boundaries

Building and running a driver are separate operations.

```text
BuildDriver
  input: source ref, dependency lock, target runtime
  output: bundle ref, manifest, diagnostics, declared capabilities

RunDriver
  input: driver version, event payload, scoped capabilities
  output: DriverRun events, actions, TaskRuns, artifacts, result
```

The build step can use Flue's existing project model:

- source files under `.flue/workflows/` and `.flue/agents/`;
- optional `.flue/app.ts` for custom ingress/routing;
- `createAgent(...)` for runtime agent definitions;
- `init(...)` and `harness.session()` for per-run sessions;
- Valibot schemas for typed results;
- sandbox connectors for local, virtual, Daytona, Cloudflare, or other
  providers.

Loom should wrap that with:

- isolated builds for untrusted code;
- dependency allowlists or locked dependency snapshots;
- capability declarations and policy checks;
- FleetDB-backed run stores and session stores;
- action-ledger mediated side effects;
- signed bundle digests so a run can prove which driver version executed.

## Registered Invocation Surfaces

Every external or internal invocation surface should become a `TriggerBinding`.
This is the generic layer that lets lead agents and users productize dynamic
drivers without requiring a new FleetDB schema for each integration.

```text
HTTP endpoint / Slack event / GitHub webhook / schedule / message bus / lead command
  -> TriggerBinding match
  -> auth/signature/idempotency validation
  -> TriggerEvent persisted
  -> DriverRun admitted for pinned DriverVersion
  -> Flue executes driver
  -> driver emits actions, replies, TaskRuns, artifacts
```

Examples:

```text
TriggerBinding
  source_kind: http
  route_key: epics.runs.create
  method: POST
  path_template: /epics/{epic_id}/runs
  driver_version_id: complete-epic@v3
  target_entrypoint: run
  auth_policy: workspace_user
  idempotency_policy: header:Idempotency-Key
  concurrency_policy: one_active_per_epic
```

```text
TriggerBinding
  source_kind: github
  event_type_patterns: issues.opened, issues.reopened
  driver_version_id: github-triage@v8
  auth_policy: github_hmac
  idempotency_policy: github_delivery_id
```

```text
TriggerBinding
  source_kind: slack
  topic: slack.message.channels.C123
  driver_version_id: support-router@v4
  auth_policy: slack_signature
  idempotency_policy: slack_event_id
```

```text
TriggerBinding
  source_kind: message_bus
  topic: agent-workflows.epic-complete
  driver_version_id: complete-epic@v3
  idempotency_policy: message_id
```

`POST /epics/{epic_id}/runs` should be a Loom-owned route whose implementation
is resolved by a binding. That keeps auth, UI semantics, idempotency, and
active-run constraints stable while still allowing teams or lead agents to swap
the underlying driver version.

## Invocation Paths

### Human Lead Agent

```text
User opens lead session
  -> Loom creates/loads AgentService(kind=lead)
  -> FleetDB service lease ensures one active owner for the lead service
  -> lead conversation/session events are persisted
  -> lead may create DriverRun or TaskRun children
  -> finite coding work runs outside the lead sandbox as TaskRuns
```

Persistent files outside the repo belong to the lead service runtime state.
Provisioning a new sandbox for every interaction is not acceptable for this
mode.

### Support Agent

```text
Customer message
  -> TriggerEvent(source=webhook|chat|manual)
  -> DriverRun(driver=support-triage@version)
  -> AgentService(kind=support) or lightweight Flue virtual sandbox
  -> response artifact/session event
  -> optional issue/task/task-run creation
```

Flue's default virtual sandbox is useful here for staged knowledge-base files
and lightweight support responses. It is not a replacement for Daytona or a
container when arbitrary repo shell execution is required.

### GitHub Issue Triage

```text
GitHub webhook
  -> Loom verifies signature
  -> FleetDB TriggerEvent(source=github)
  -> TriggerDelivery dedupes by delivery ID/event/object
  -> DriverRun(driver=issue-triage@version)
  -> driver TypeScript evaluates event and emits workflow actions
  -> optional TaskRun for code investigation or fix
  -> ActionLedger records comments/labels/PR/task creation
```

### CI Remediation

```text
CI job or webhook
  -> TriggerEvent(source=ci)
  -> DriverRun(driver=ci-remediation@version)
  -> runner placement may be the CI host using Flue local()
  -> TaskRun reports logs, artifacts, status through scoped capability
  -> completion policy may create PR/comment instead of closing a task
```

In CI, the runner can use the host filesystem and tools as its sandbox because
the CI system is already the isolation boundary.

### Scheduled Maintenance

```text
Scheduler tick
  -> deterministic TriggerEvent id: schedule:<trigger_id>:<fire_time>
  -> TriggerDelivery applies concurrency policy
  -> DriverRun starts
  -> driver creates task, comment, report, or TaskRun
```

Schedules should be durable and replayable. Missed ticks should become visible
delivery states, not silent gaps.

### FleetDB Dependency-Driven Epic Runner

```text
FleetDB ready frontier
  -> scheduler atomically creates TaskRun for an unblocked task
  -> TaskRun lease acquired
  -> runner executes exact task
  -> artifacts finalized
  -> CompleteRun validates lease, task version, dependency projection, artifacts
  -> FleetDB task transition closes/reviews/blocks
  -> FleetDB dependency projection exposes next ready frontier
```

This remains the center of coding-task scale-out.

## TypeScript Developer Model

V2 needs three TypeScript surfaces.

### Driver Authoring Surface

For users, teams, and lead agents writing dynamic workflow drivers.

```ts
import { createAgent, type FlueContext } from "@flue/runtime";
import { loom } from "@loom/sdk/flue";

export async function run(ctx: FlueContext) {
  const event = loom.event(ctx);
  const agent = createAgent(() => ({
    model: "openai/gpt-5.5",
    sandbox: loom.sandbox.forEvent(event),
  }));

  const harness = await ctx.init(agent);
  const session = await harness.session();
  const result = await session.prompt(event.payload.prompt);

  await loom.actions.recordResult({
    result,
    idempotencyKey: event.key("result"),
  });

  return result;
}
```

This is the core dynamic workflow model. A `.ts` file becomes a versioned
`DriverVersion` after build and validation. The driver can create agents,
choose sandboxes, call skills, branch on typed model output, and emit actions.
FleetDB stores the run envelope and action results, not the driver's internal
control flow.

### App/Control SDK

For application code that registers drivers, manages bindings, or invokes
driver runs.

```ts
import { Loom } from "@loom/sdk";

const loom = new Loom({ workspace: "acme" });

const driver = await loom.drivers.publish({
  name: "github-issue-triage",
  source: { repo: "org/app", path: ".loom/workflows/triage.ts", ref: "main" },
  activate: false,
});

await loom.bindings.create({
  name: "github-issue-triage",
  sourceKind: "github",
  eventTypePatterns: ["issues.opened", "issues.reopened"],
  driverVersionId: driver.versionId,
  entrypoint: "run",
  concurrencyPolicy: "queue",
});
```

The control SDK should not require every provider or workflow variant to be a
new FleetDB type. Provider-specific config and driver-specific input schemas can
live in versioned JSON refs while common fields stay queryable.

### Runner SDK

For code running inside a leased TaskRun.

```ts
import { TaskRunClient } from "@loom/sdk/runner";

const run = TaskRunClient.fromEnv();

const task = await run.getTask();
await run.heartbeat();
await run.logs.append({ stream: "stdout", text: "starting" });

const artifact = await run.artifacts.declare({
  type: "patch",
  contentHash,
  sizeBytes,
  idempotencyKey,
});

await artifact.upload(bytes);
await artifact.finalize();

await run.completeRun({
  completionId,
  artifactIds: [artifact.id],
  taskStatusPolicy: { action: "close" },
});
```

The runner SDK must not expose broad task mutation. It operates against one
scoped run, lease, and fencing token.

## Local And Cloud Deployment Modes

### Local User FleetDB

```text
local UI / CLI / daemon
  -> embedded or local FleetDB
  -> local runner owns FleetDB-facing writes
  -> remote Daytona sandbox receives task context, not FleetDB credentials
  -> local patch-back applies to developer worktree
  -> local FleetDB task close unlocks dependents
```

Rules:

- only the trusted local Loom process talks to embedded FleetDB;
- do not pass loopback FleetDB URLs or raw FleetDB credentials into Daytona;
- if patch-back conflicts with local user edits, preserve the patch artifact
  and do not close the task;
- local mode must use the same FleetDB resource model as cloud mode.

### Cloud FleetDB Deployment

```text
Loom API / scheduler
  -> FleetDB data layer
  -> scoped runner token minted for one TaskRun or AgentService
  -> remote runner starts in CI, Kubernetes, container, or Daytona bootstrap
  -> runner calls Loom/FleetDB-backed TaskRun APIs
  -> artifacts finalize in server-visible storage
  -> CompleteRun updates FleetDB and dependency projection
```

Rules:

- runners do not receive raw FleetDB credentials;
- all runner writes are scoped and fenced;
- artifacts must be readable after sandbox cleanup;
- cloud scale-out is capacity-provider agnostic.

## Push Model vs Pull Model

V2 should use a pull-authoritative model with push-assisted latency.

### Pull Authority

Runners and services repeatedly call FleetDB-backed APIs to:

- acquire leases;
- renew heartbeats;
- fetch commands;
- claim a ready TaskRun;
- append logs/events;
- finalize artifacts;
- complete or fail a run.

If push infrastructure drops events, the system should still converge.

### Push Assistance

Push can be used for:

- wake a runner when a command is available;
- notify UI of run progress;
- stream logs and transcripts;
- deliver cancellation hints;
- reduce scheduling latency after a task closes.

Push must not be the only record of work, state, or authority.

## Result Flow Back To FleetDB

Every finite coding run should converge through this sequence:

```text
runner starts
  -> FleetDB TaskRun status = running
  -> logs/transcript events appended
  -> artifacts declared
  -> artifacts uploaded/finalized with hashes
  -> runner calls CompleteRun
  -> FleetDB validates lease/fencing
  -> FleetDB validates required artifacts
  -> FleetDB validates task/dependency preconditions
  -> FleetDB writes action ledger entries
  -> FleetDB transitions task/run/session state atomically
  -> dependency projection updates ready frontier
```

`CompleteRun` should be idempotent by `completion_id`. Retried completion should
return the existing outcome if the previous attempt already committed.

## Runtime Provider Contract

V2 records runner placement and sandbox placement separately.

```text
runner_placement:
  provider: local | daemon | ci | podman | kubernetes | daytona_bootstrap
  node_id
  runner_id
  process_ref
  started_at
  heartbeat_at

sandbox_placement:
  provider: local | flue_virtual | flue_local | daytona | container | kubernetes
  sandbox_id
  image_or_snapshot
  cwd
  repo_ref
  cleanup_policy
  retained_until
```

Provider adapters handle:

- provisioning;
- repo hydration;
- command execution;
- cancellation;
- artifact collection;
- cleanup;
- capacity accounting;
- secret delivery.

FleetDB and Loom still own:

- task selection;
- trigger events;
- leases;
- run state;
- artifacts metadata;
- dependency unlocks.

## Flue And Daytona In V2

Flue is the model/tool harness. Daytona is one possible remote Linux sandbox.
Neither should become the platform data layer.

### Flue Source Code Findings

Reading `../flue` changes the recommendation. Flue already has the right shape
for dynamic TypeScript drivers:

- workflow modules are source files under `.flue/workflows/*.ts` that export
  `run(ctx)`;
- agent modules are source files under `.flue/agents/*.ts` that export
  `createAgent(...)`;
- `flue build` discovers those files and generates a manifest-backed server;
- `flue run` builds a project, starts a temporary local server, invokes one
  workflow, streams events, returns JSON, and exits;
- workflows produce inspectable run IDs, durable run events when a run store is
  configured, SSE streams, and typed result envelopes;
- direct/message-driven agents are addressable by agent name and instance ID,
  can receive many messages over time, and are intentionally separate from
  workflow runs;
- custom `app.ts` ingress code can verify provider webhooks and call
  `dispatch(...)` into agent instances;
- sandbox choice is runtime code: default virtual sandbox, `local()`, Daytona,
  Cloudflare sandbox, or custom connector;
- custom tools, skills, subagents, result schemas, and sandbox connectors are
  code-level extensions.

That means Loom should use Flue as the driver execution/build substrate instead
of inventing a static workflow DSL.

Flue also has limits Loom must cover:

- discovery is build-manifest based, so new `.ts` drivers need a build/publish
  step before production use;
- Node run/session stores default to in-memory process lifetime;
- Cloudflare run/session state uses Durable Object storage by default, not
  FleetDB;
- direct agent interactions do not create Flue workflow runs, so Loom must map
  them to `AgentService` activity/session records when product visibility
  requires it;
- `dispatch(...)` returns admission, not completion, and provider retry
  idempotency remains application responsibility;
- Flue run status is coarse compared with Loom's needed task lease,
  dependency, artifact, and completion state;
- arbitrary TypeScript drivers are arbitrary code execution and require build
  isolation, capability scoping, dependency policy, review, and audit;
- Flue does not own FleetDB dependency closure, task leases, action-ledger side
  effects, or durable artifact validation.

The V2 integration posture should be:

```text
FleetDB stores driver versions, invocations, actions, leases, artifacts
Loom validates, builds, dispatches, and governs drivers
Flue executes the TypeScript driver and emits events/results
FleetDB remains the durable data layer
```

Use Flue in three ways:

1. **Flue local()**
   Useful for local development and CI where the host is already the isolation
   boundary.

2. **Flue default virtual sandbox**
   Useful for support and lightweight knowledge-base workflows that need staged
   files but not arbitrary repo shell execution.

3. **Flue + Daytona**
   Useful for isolated coding tasks and persistent lead/support services that
   need a durable Linux workspace.

For persistent lead agents, Daytona sandbox lifecycle belongs to the
`AgentService`, not to individual child tasks. For task fanout, each child
coding attempt should use its own `TaskRun` and task sandbox unless an explicit
warm-pool policy is introduced.

## Gaps To Close

### FleetDB Data Model Gaps

- Add `Driver` and `DriverVersion` for dynamic `.ts` drivers.
- Add `TriggerBinding`, `TriggerEvent`, and `TriggerDelivery`.
- Add `DriverRun` and `DriverStep`.
- Promote `TaskRun` to a first-class resource instead of overloading
  `AgentSession`.
- Add generic `Lease` with fencing for task runs, services, driver runs, and
  artifact uploads.
- Add `ActionLedger` for idempotent external side effects.
- Define artifact metadata and upload/finalize states.
- Split current agent definitions into `AgentService` and `WorkerProfile`.

### API Gaps

- Public APIs for driver draft creation, source upload/snapshot, build,
  validation, activation, rollback, and invocation.
- Public APIs for trigger bindings, events, driver runs, task runs, leases,
  artifacts, and action ledger.
- Runner-scoped APIs for heartbeat, logs, usage, artifact upload, and
  `CompleteRun`.
- Webhook ingestion endpoints with signature verification and replay protection.
- Schedule management and durable scheduler tick records.
- Event stream APIs for UI visibility.

### Runtime Gaps

- Isolated build workers for user-authored and lead-authored TypeScript.
- Driver execution workers that can run a pinned bundle digest with scoped
  capabilities.
- One canonical run command contract:
  `run_task`, `run_prompt`, `run_workflow`, `resume_session`, `cancel_run`.
- Runner bootstrap that carries server URL, run ID, lease ID, fencing token, and
  scoped token.
- Separate runner placement and sandbox placement metadata.
- Remote artifact durability before sandbox cleanup.
- Cleanup and retention policy for persistent services and ephemeral tasks.

### SDK Gaps

- Driver authoring SDK for Flue-backed workflow code.
- App/control SDK for driver publishing, binding management, and invocation.
- Runner SDK for one scoped TaskRun.
- Typed result schemas for driver steps.
- Local and cloud auth modes with the same resource model.

### Security Gaps

- Tenant/workspace scoping on every durable row and token claim.
- Source/bundle signing, digest verification, and dependency-lock validation for
  dynamic drivers.
- Build sandboxing and policy gates for lead-authored code.
- Short-lived scoped runner tokens.
- No raw FleetDB credentials in remote sandboxes.
- HMAC verification and replay windows for webhooks.
- Artifact hash validation, redaction, and secret scanning.
- Audit events for privileged reads, writes, leases, and side effects.

### UI/Ops Gaps

- Driver registry, version history, build logs, validation errors, activation,
  rollback, and approval UI.
- Trigger binding and delivery timeline.
- Driver run timeline.
- TaskRun detail page with lease, runner, sandbox, artifacts, and retries.
- AgentService page for persistent lead/support agents.
- Stuck lease and stale runner recovery actions.
- Dependency frontier visualization tied to FleetDB.

## Phased Implementation Plan

### Phase 0: Taxonomy And Contract

- Adopt FleetDB-as-data-layer wording across the docs.
- Declare `Driver`/`DriverVersion` as the dynamic TypeScript driver
  contract.
- Declare `TaskRun` as the finite execution unit.
- Declare `AgentService` as the long-running agent unit.
- Declare `DriverRun` as the durable triggered workflow execution unit.
- Decide whether Loom API is a facade over FleetDB APIs or whether FleetDB
  exposes these control-plane APIs directly.

### Phase 1: FleetDB Platform Schema

- Add driver, binding, driver-run, task-run, lease, artifact, and action-ledger
  resources to FleetDB.
- Add migrations and store interfaces.
- Keep existing `AgentSession` as compatibility telemetry.
- Add idempotency keys to creation and completion paths.

### Phase 2: Dynamic Driver MVP

- Let a user or lead agent create a `.ts` driver draft.
- Snapshot source and dependency metadata.
- Build it through Flue in an isolated local build worker.
- Store `DriverVersion` manifest, bundle ref, diagnostics, and digest in
  FleetDB.
- Invoke the driver manually as a `DriverRun`.
- Persist driver logs/events/results through FleetDB-backed run records.

### Phase 3: Minimal Binding And DriverRun MVP

- Implement manual trigger, schedule trigger, GitHub issue webhook, CI webhook,
  and generic webhook.
- Persist `TriggerEvent` before dispatch.
- Add `DriverRun` creation and delivery status.
- Target activated `DriverVersion`s from `TriggerBinding`.
- Bridge driver-emitted run actions into existing `AgentCommand start` where
  possible.
- Record the parent/child link from `DriverRun` to `AgentSession` or
  `TaskRun`.

### Phase 4: TaskRun Completion Contract

- Add runner-scoped bootstrap tokens.
- Add heartbeat, logs, usage, artifact declare/upload/finalize APIs.
- Add `CompleteRun` with lease/fencing validation.
- Make FleetDB dependency unlock happen only through accepted completion policy.
- Add stale runner, lost sandbox, duplicate completion, and artifact failure
  recovery states.

### Phase 5: Runtime Scale-Out

- Add runtime provider adapters for local, CI, Kubernetes/container, and
  Daytona.
- Support hundreds of concurrent TaskRuns from FleetDB's ready frontier.
- Add capacity reservations, retry policies, quotas, and cancellation.
- Keep persistent lead/support sandboxes as `AgentService` runtime state.

### Phase 6: Productize Visibility And Operations

- Add UI for drivers, versions, bindings, driver runs, task runs, agent
  services, artifacts, dependency frontier, and recovery actions.
- Add audit logs and billing/usage rollups.
- Add replay tools for failed trigger deliveries.
- Add runbooks for local, cloud, and hybrid worker deployment.

## Minimal V2 Vertical Slice

The smallest useful V2 slice is:

1. FleetDB stores `Driver`, `DriverVersion`, `TriggerBinding`, `TriggerEvent`,
   `DriverRun`, `TaskRun`, `Lease`, `Artifact`, and `ActionLedger` records.
2. A user or lead agent creates `.loom/workflows/triage.ts`.
3. Loom builds the driver with Flue, stores the bundle digest and manifest, and
   activates the `DriverVersion`.
4. Loom exposes one manual binding and one schedule binding.
5. The binding creates a `DriverRun` targeting the activated driver version.
6. The driver creates one `TaskRun` for an existing FleetDB task.
7. A local runner or Flue local runner executes the task.
8. The runner appends logs and finalizes a patch artifact.
9. `CompleteRun` validates the lease and artifact.
10. FleetDB closes the task and updates dependency readiness.
11. The UI shows the driver version, binding, driver run, task run, artifact,
   and dependency
   unlock as one timeline.

This proves the platform shape before scaling to GitHub, CI, custom TypeScript,
Daytona, or Kubernetes.

## Edge Cases V2 Must Handle

| Edge case | Required behavior |
|---|---|
| Lead writes invalid TypeScript | Build fails; DriverVersion remains inactive with diagnostics |
| Driver requests disallowed import/capability | Validation fails or activation requires approval |
| Driver source changes after build | Existing DriverVersion still points to immutable source/bundle digest |
| Activated driver has a bug | DriverRun fails; previous DriverVersion can be rolled back |
| Driver emits unknown step kind | FleetDB stores it as generic step data; UI shows fallback rendering |
| Driver emits duplicate action | ActionLedger returns existing result by idempotency key |
| Duplicate webhook delivery | Same `TriggerEvent`/`TriggerDelivery` or duplicate status; no duplicate side effect |
| Missed schedule tick | Visible missed/late delivery; deterministic replay possible |
| Driver TypeScript throws before dispatch | DriverRun fails; no TaskRun created unless action committed |
| Driver retries comment action | ActionLedger returns existing comment result |
| Runner dies mid-run | Lease expires; TaskRun becomes stale/lost; task not closed |
| Runner loses lease during finalization | Stale `CompleteRun` rejected unless same committed completion ID |
| Artifact upload fails | TaskRun cannot close if required artifact is missing |
| Sandbox cleanup fails after success | Run stays completed; cleanup state remains pending/retained |
| Daytona cannot reach local FleetDB | Expected in local mode; host runner bridges through scoped context |
| Local patch-back conflicts | Preserve patch artifact; do not close task |
| CI runner has broad repo access | Loom token still scopes FleetDB mutations to one run |
| Lead sandbox has persistent state | Service lifecycle preserves sandbox; child coding work uses TaskRuns |
| Push event dropped | Pull loop eventually observes command/status |
| Two schedulers race ready frontier | FleetDB atomic TaskRun/lease creation admits one winner |
| Task closes while dependency projection stale | `CompleteRun` validates task/dependency projection version |
| User cancels run | Desired cancel recorded; provider kill best effort; completion cannot close task unless policy allows |

## Open Questions

1. Should FleetDB expose the V2 control-plane API directly, or should Loom
   server expose it while writing through FleetDB internals?
2. Should dynamic drivers use Flue project layout directly (`.flue/`) or a
   Loom-owned layout (`.loom/workflows`) that compiles to Flue?
3. What approval policy is required before a lead-authored driver becomes
   active?
4. Should `AutomationRun`, `EpicRun`, and `SupportRun` remain product-specific
   views over `DriverRun`, or should the product expose only `DriverRun`?
5. Should `AgentSession{Kind=task}` be a compatibility view over `TaskRun`, or
   should both records exist during migration?
6. What is the minimum object-storage abstraction for local and cloud artifact
   durability?
7. How much custom TypeScript should run inside Loom server versus an isolated
   workflow runner?
8. Which external side effects are allowed in V2 without human approval:
   comments, labels, PR creation, task close, merge?
9. What is the first cloud runtime target after local/CI: Daytona bootstrap,
   Kubernetes pod, or container worker?
10. How should local embedded FleetDB state migrate into cloud FleetDB, if at
   all?

## Recommendation

Build V2 FleetDB-first:

1. Put driver, binding, driver-run, task-run, lease, artifact, and
   action-ledger records in FleetDB.
2. Treat Loom server and SDKs as the control plane over those FleetDB resources.
3. Use Flue as the dynamic TypeScript driver runtime/build substrate.
4. Make all invocation paths create durable FleetDB-backed intent first.
5. Make all finite coding work complete through fenced TaskRun completion.
6. Keep Daytona, local, CI, and Kubernetes behind runtime adapters.

This gives Loom a scalable platform shape: teams can invoke agents however they
want, FleetDB keeps the durable state and dependency semantics coherent, and
runtime providers can be swapped without changing the product model.
