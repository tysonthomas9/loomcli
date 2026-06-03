# FleetDB-Backed Loom Agent Platform V2 Proposal

**Status:** V2 proposal for review
**Date:** 2026-06-03
**Related:**
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
  - trigger definitions and events
  - automation and workflow runs
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
| Durable center | TaskRun | TriggerEvent, AutomationRun, AgentService, TaskRun |
| Invocation sources | FleetDB ready frontier | Human, lead, schedule, GitHub, CI, webhook, custom TS, ready frontier |
| Long-running agents | Mentioned as separate | First-class AgentService |
| TypeScript | Runner SDK | App/automation SDK plus runner SDK |
| Data layer | FleetDB for tasks/runs | FleetDB for platform state |
| Runtime providers | Flue + Daytona | Local, CI, Flue, Daytona, containers, Kubernetes |
| Scale-out | Phase 4 task sandboxes | Platform-level scheduler over FleetDB |

## Non-Negotiable Principles

1. **FleetDB is the system of record.**
   FleetDB owns durable platform state: tasks, dependencies, triggers, events,
   automation runs, task runs, leases, sessions, artifact metadata, and action
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
   long-running `AgentService`s or `AutomationRun`s. They create `TaskRun`s only
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
                         | Custom TypeScript           |
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
| TriggerDefinition / TriggerEvent / TriggerDelivery                    |
| AutomationRun / WorkflowRun / WorkflowStep                            |
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
| Trigger definitions | FleetDB | Schedules, GitHub, CI, webhooks, manual/custom TS |
| Trigger events | FleetDB | Normalized, deduped, replayable event records |
| Automation/workflow runs | FleetDB | Durable response to one event or manual request |
| Agent services | FleetDB | Long-running desired agents and service leases |
| Worker profiles | FleetDB | Reusable finite-task execution policy |
| Task runs | FleetDB | One finite attempt against one task or workflow step |
| Leases/fencing | FleetDB | Authority for runner mutations |
| Nodes/runners | FleetDB | Observed runtime capacity and heartbeat state |
| Sessions | FleetDB | Telemetry index, transcript/session metadata |
| Artifact metadata | FleetDB | Type, hash, size, refs, retention, visibility |
| Artifact blobs | Object store or local store | Referenced by FleetDB metadata |
| Action ledger | FleetDB | Idempotent side effects and external writes |
| Local PIDs, sockets, temp dirs | Node-local runtime | Never global truth |
| Sandbox provider IDs | Runtime metadata in FleetDB | Useful for diagnostics, not scheduling truth |

## Core FleetDB Entities

### TriggerDefinition

Defines how external or internal events enter Loom.

```text
TriggerDefinition
  id
  workspace_key
  name
  source_type: manual | schedule | github | ci | webhook | custom_ts | fleetdb_ready
  source_ref
  event_types
  filters
  target_type: automation | workflow | agent_service | task_run
  target_ref
  concurrency_policy: allow | forbid | replace | queue
  idempotency_template
  permissions
  enabled
  created_at
  updated_at
```

Examples:

- nightly repo maintenance schedule;
- GitHub issue opened triage;
- CI failure remediation;
- support escalation webhook;
- custom TypeScript workflow entrypoint.

### TriggerEvent

Immutable record that an event happened.

```text
TriggerEvent
  id
  workspace_key
  trigger_definition_id optional
  source_type
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
  trigger_definition_id
  status: accepted | rejected | duplicate | queued | dispatched | failed | replayed
  rejection_reason optional
  automation_run_id optional
  attempt
  next_retry_at optional
  error_class optional
  created_at
  updated_at
```

### AutomationRun

The durable parent for "respond to this event/request".

```text
AutomationRun
  id
  workspace_key
  trigger_event_id optional
  requested_by
  source_type
  source_ref
  workflow_name
  status: queued | running | waiting | completed | failed | cancelled
  concurrency_key
  idempotency_key
  lease_id optional
  started_at
  ended_at
  summary
```

An `AutomationRun` may create issues, comments, workflow steps, agent service
commands, or task runs. It is the right parent for GitHub/CI/schedule/custom TS
flows.

### WorkflowStep

Auditable step under an automation run.

```text
WorkflowStep
  id
  automation_run_id
  kind: evaluate | plan | run_agent | create_task | comment | create_pr | wait | gate
  status: queued | running | waiting | completed | failed | skipped
  task_run_id optional
  action_ledger_id optional
  input_ref
  output_ref
  started_at
  ended_at
```

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

One finite execution attempt for one task or workflow step.

```text
TaskRun
  id
  workspace_key
  task_id optional
  automation_run_id optional
  workflow_step_id optional
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
  resource_type: agent_service | automation_run | task_run | terminal | artifact_upload
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
  owner_type: task_run | automation_run | agent_service | session
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

## Invocation Paths

### Human Lead Agent

```text
User opens lead session
  -> Loom creates/loads AgentService(kind=lead)
  -> FleetDB service lease ensures one active owner for the lead service
  -> lead conversation/session events are persisted
  -> lead may create AutomationRun or TaskRun children
  -> finite coding work runs outside the lead sandbox as TaskRuns
```

Persistent files outside the repo belong to the lead service runtime state.
Provisioning a new sandbox for every interaction is not acceptable for this
mode.

### Support Agent

```text
Customer message
  -> TriggerEvent(source=webhook|chat|manual)
  -> AutomationRun(workflow=support-triage)
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
  -> AutomationRun(workflow=issue-triage)
  -> custom TS evaluates event and emits workflow actions
  -> optional TaskRun for code investigation or fix
  -> ActionLedger records comments/labels/PR/task creation
```

### CI Remediation

```text
CI job or webhook
  -> TriggerEvent(source=ci)
  -> AutomationRun(workflow=ci-remediation)
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
  -> AutomationRun starts
  -> workflow creates task, comment, report, or TaskRun
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

V2 needs two TypeScript surfaces.

### App/Automation SDK

For teams writing schedules, GitHub handlers, CI integrations, and custom
automation.

```ts
import { Loom, defineTrigger, defineWorkflow } from "@loom/sdk";

export default defineTrigger({
  name: "github-issue-triage",
  source: "github",
  on: ["issues.opened", "issues.reopened"],
  workflow: defineWorkflow(async (ctx) => {
    const issue = await ctx.github.issue();

    if (issue.labels.includes("security")) {
      await ctx.action.comment({
        body: "Security triage has started.",
        idempotencyKey: ctx.event.key("security-comment"),
      });
    }

    return ctx.runAgent({
      agent: "triage",
      input: { issueNumber: issue.number },
      idempotencyKey: ctx.event.key("triage-agent"),
    });
  }),
});
```

The automation SDK should let custom TypeScript inspect normalized event context
and emit workflow actions. Loom should execute side effects through FleetDB
leases and the action ledger.

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

- Add `TriggerDefinition`, `TriggerEvent`, and `TriggerDelivery`.
- Add `AutomationRun` and `WorkflowStep`.
- Promote `TaskRun` to a first-class resource instead of overloading
  `AgentSession`.
- Add generic `Lease` with fencing for task runs, services, automations, and
  artifact uploads.
- Add `ActionLedger` for idempotent external side effects.
- Define artifact metadata and upload/finalize states.
- Split current agent definitions into `AgentService` and `WorkerProfile`.

### API Gaps

- Public APIs for triggers, events, automation runs, task runs, leases,
  artifacts, and action ledger.
- Runner-scoped APIs for heartbeat, logs, usage, artifact upload, and
  `CompleteRun`.
- Webhook ingestion endpoints with signature verification and replay protection.
- Schedule management and durable scheduler tick records.
- Event stream APIs for UI visibility.

### Runtime Gaps

- One canonical run command contract:
  `run_task`, `run_prompt`, `run_workflow`, `resume_session`, `cancel_run`.
- Runner bootstrap that carries server URL, run ID, lease ID, fencing token, and
  scoped token.
- Separate runner placement and sandbox placement metadata.
- Remote artifact durability before sandbox cleanup.
- Cleanup and retention policy for persistent services and ephemeral tasks.

### SDK Gaps

- App/automation SDK for triggers, workflows, events, and action emission.
- Runner SDK for one scoped TaskRun.
- Typed result schemas for workflow steps.
- Local and cloud auth modes with the same resource model.

### Security Gaps

- Tenant/workspace scoping on every durable row and token claim.
- Short-lived scoped runner tokens.
- No raw FleetDB credentials in remote sandboxes.
- HMAC verification and replay windows for webhooks.
- Artifact hash validation, redaction, and secret scanning.
- Audit events for privileged reads, writes, leases, and side effects.

### UI/Ops Gaps

- Trigger delivery timeline.
- Automation run timeline.
- TaskRun detail page with lease, runner, sandbox, artifacts, and retries.
- AgentService page for persistent lead/support agents.
- Stuck lease and stale runner recovery actions.
- Dependency frontier visualization tied to FleetDB.

## Phased Implementation Plan

### Phase 0: Taxonomy And Contract

- Adopt FleetDB-as-data-layer wording across the docs.
- Declare `TaskRun` as the finite execution unit.
- Declare `AgentService` as the long-running agent unit.
- Declare `AutomationRun` as the durable triggered workflow unit.
- Decide whether Loom API is a facade over FleetDB APIs or whether FleetDB
  exposes these control-plane APIs directly.

### Phase 1: FleetDB Platform Schema

- Add trigger, automation, task-run, lease, artifact, and action-ledger
  resources to FleetDB.
- Add migrations and store interfaces.
- Keep existing `AgentSession` as compatibility telemetry.
- Add idempotency keys to creation and completion paths.

### Phase 2: Minimal Trigger And Automation MVP

- Implement manual trigger, schedule trigger, GitHub issue webhook, CI webhook,
  and generic webhook.
- Persist `TriggerEvent` before dispatch.
- Add `AutomationRun` creation and delivery status.
- Bridge workflow actions into existing `AgentCommand start` where possible.
- Record the parent/child link from `AutomationRun` to `AgentSession` or
  `TaskRun`.

### Phase 3: TaskRun Completion Contract

- Add runner-scoped bootstrap tokens.
- Add heartbeat, logs, usage, artifact declare/upload/finalize APIs.
- Add `CompleteRun` with lease/fencing validation.
- Make FleetDB dependency unlock happen only through accepted completion policy.
- Add stale runner, lost sandbox, duplicate completion, and artifact failure
  recovery states.

### Phase 4: Runtime Scale-Out

- Add runtime provider adapters for local, CI, Kubernetes/container, and
  Daytona.
- Support hundreds of concurrent TaskRuns from FleetDB's ready frontier.
- Add capacity reservations, retry policies, quotas, and cancellation.
- Keep persistent lead/support sandboxes as `AgentService` runtime state.

### Phase 5: Productize Visibility And Operations

- Add UI for triggers, automation runs, task runs, agent services, artifacts,
  dependency frontier, and recovery actions.
- Add audit logs and billing/usage rollups.
- Add replay tools for failed trigger deliveries.
- Add runbooks for local, cloud, and hybrid worker deployment.

## Minimal V2 Vertical Slice

The smallest useful V2 slice is:

1. FleetDB stores `TriggerEvent`, `AutomationRun`, `TaskRun`, `Lease`,
   `Artifact`, and `ActionLedger` records.
2. Loom exposes a manual trigger API and one schedule trigger.
3. The trigger creates an `AutomationRun`.
4. The automation creates one `TaskRun` for an existing FleetDB task.
5. A local runner or Flue local runner executes the task.
6. The runner appends logs and finalizes a patch artifact.
7. `CompleteRun` validates the lease and artifact.
8. FleetDB closes the task and updates dependency readiness.
9. The UI shows the trigger, automation, task run, artifact, and dependency
   unlock as one timeline.

This proves the platform shape before scaling to GitHub, CI, custom TypeScript,
Daytona, or Kubernetes.

## Edge Cases V2 Must Handle

| Edge case | Required behavior |
|---|---|
| Duplicate webhook delivery | Same `TriggerEvent`/`TriggerDelivery` or duplicate status; no duplicate side effect |
| Missed schedule tick | Visible missed/late delivery; deterministic replay possible |
| Custom TS throws before dispatch | AutomationRun fails; no TaskRun created unless action committed |
| Workflow retries comment action | ActionLedger returns existing comment result |
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
2. Should `AutomationRun` and `WorkflowRun` be one table or separate concepts?
3. Should `AgentSession{Kind=task}` be a compatibility view over `TaskRun`, or
   should both records exist during migration?
4. What is the minimum object-storage abstraction for local and cloud artifact
   durability?
5. How much custom TypeScript should run inside Loom server versus an isolated
   workflow runner?
6. Which external side effects are allowed in V2 without human approval:
   comments, labels, PR creation, task close, merge?
7. What is the first cloud runtime target after local/CI: Daytona bootstrap,
   Kubernetes pod, or container worker?
8. How should local embedded FleetDB state migrate into cloud FleetDB, if at
   all?

## Recommendation

Build V2 FleetDB-first:

1. Put trigger, automation, task-run, lease, artifact, and action-ledger records
   in FleetDB.
2. Treat Loom server and SDKs as the control plane over those FleetDB resources.
3. Make all invocation paths create durable FleetDB-backed intent first.
4. Make all finite coding work complete through fenced TaskRun completion.
5. Keep Flue, Daytona, local, CI, and Kubernetes behind runtime adapters.

This gives Loom a scalable platform shape: teams can invoke agents however they
want, FleetDB keeps the durable state and dependency semantics coherent, and
runtime providers can be swapped without changing the product model.
