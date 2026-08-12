# Agent Data-Model Inventory and Migration

**Status:** Proposed implementation plan

**Inventory snapshot:** 2026-07-09

**Loom source:** `loomcli` branch `unified-agents` compared with `origin/v5`

**FleetDB source:** sibling `fleet-db` branch `unified-agents` compared with `origin/main`

**Flue source:** local `withastro/flue` checkout at Loom pin `492bf47b9f3d6c379d00471523987b8fe9511f7d`

**Architecture:** [AI SDLC Agent Control Plane](2026-07-09-ai-sdlc-agent-control-plane-architecture.md)

**Decision:** [ADR-0001](../adr/0001-agent-identity-role-policy-flow-and-deployment.md)

**Vocabulary:** [Loom glossary](../loom-glossary.md)

## Purpose and scope

This document answers two questions:

1. What agent, SDLC, orchestration, runtime, evidence, and integration models
   exist on the current Loom/FleetDB branches?
2. How do those records migrate to the target architecture without losing
   identity, history, audit, task correctness, or external references?

The inventory distinguishes:

- **durable FleetDB records**, which are shared control-plane truth;
- **embedded/value models**, which are serialized as part of another record;
- **Loom wire mirrors**, which repeat FleetDB shapes but are not another
  conceptual database model;
- **legacy Loom-local durable records**, principally filesystem sessions and
  stack lineage;
- **integrated external harness models**, which must be mapped without becoming
  competing canonical product schemas;
- **runtime-only structures and operation results**, which must not be promoted
  to first-class records merely because they are Go structs.

Request DTOs, list filters, handlers, stores, controllers, test fixtures, and
provider parser internals are not independent domain records. They are listed
where necessary to account for serialized compatibility, but the migration does
not create one table for every exported struct.

## Branch delta in architectural terms

### Loom against v5

The branch adds a broad unified-agent product layer: a combined Agents API/UI,
prompt and scripted workflow agents, role seeding and editing, AgentService and
TriggerBinding lifecycle endpoints, connector provisioning, direct run history,
workflow source display, task-ready loopback routing, global runner behavior,
and a pinned FleetDB OpenAPI contract.

The important new data behavior is:

- `/agents` projects old supervised `Agent` rows, `AgentService` records, and
  some unattached `TriggerBinding` records into one UI collection;
- prompt agents are materialized as coordinated Role, AgentService, Driver,
  DriverVersion, and TriggerBinding records;
- DriverRun receives TriggerBinding and AgentService attribution;
- task status journal entries can be converted to `internal.task.ready` events;
- connector and binding configuration enters the agent authoring flow;
- startup healing is required to restore consistency among records written by
  separate APIs.

### FleetDB against main

The branch extends the platform contract rather than introducing a replacement
domain. It adds or completes AgentService soft deletion, TriggerBinding deletion,
DriverRun binding/service attribution, connector/grant/audit APIs, artifact
content behavior, permissions, storage indexes, and PostgreSQL migrations 036
and 037 for run attribution and the agent-identity wave.

Most V2 models already exist on `main`; the branch makes Loom's unified-agent
projection possible. That makes this the right point to correct the end-state
boundary before more product code treats AgentService as permanent identity.

## Current FleetDB durable record inventory

The source of truth for this section is `fleet-db/internal/models` on the
snapshot branch.

### Workspace and configuration

| Model | Current responsibility | Main architectural issue |
|---|---|---|
| `Workspace` | Workspace identity, display/config metadata, lifecycle. | Retain; becomes the main policy and SDLC namespace. |
| `Repo` | First-class repository registration and provider/local metadata. | Retain; link to normalized repository SDLC refs. |
| `Role` | Prompt, model, backend, effort, filters, path scope, skills, tool lists, priority/concurrency, read-only, and budget. | Mixes responsibility, behavior defaults, and enforcement; needs revisioning and PolicySet split. |
| `Agent` | Persistent named persona/assignment with Role, backend fallback, repo scope, mode, filter, concurrency, budget, desired/observed state, parent, and derived liveness. | Closest identity seed, but still mixes identity, policy, deployment, and observed runtime. |
| `DaemonProfile` | Workspace daemon/supervisor configuration. | Separate organization policy from node-local runtime profile. |

`RestartPolicy` and `OTelSettings` are embedded/value configuration within the
daemon profile, not independent identities.

There is currently no durable Organization, Principal, or membership model in
`internal/models`. `auth.Role` is a permission-tier enum/claim and is distinct
from the workspace-scoped agent Role. The target organizational policy boundary
therefore requires new tenant/principal references rather than overloading
either current Role.

### SDLC work graph and journal

| Model | Current responsibility | Target disposition |
|---|---|---|
| `Issue` | Loom-managed work item with status, type, priority, assignee, design, labels, deferral, and lifecycle fields. | Retain. It remains canonical for Loom ready work and can link to external SDLC refs. |
| `Dependency` | Directed blocks, parent-child, related, duplicate-of, or superseded-by edge between Issues. | Retain; optionally expose through the generic SDLC relationship projection. |
| `Comment` | Immutable issue annotation. | Retain; add actor/run provenance where missing. |
| `Event` | Append-only mutation journal entry for supported entities/actions. | Retain as FleetDB mutation journal; do not confuse with TranscriptEvent or TriggerEvent. |
| `Snapshot` | Archived/reconstruction state snapshot. | Retain as storage/recovery mechanism. |
| `IdempotencyRecord` | Request idempotency result and expiry. | Retain; use separate namespaces for run intent and provider-event deduplication. |

### Legacy fleet worker records

| Model | Current responsibility | Target disposition |
|---|---|---|
| `Worker` | Per-claim legacy remote worker identity/state. | Retire after all executors use Node + TaskRun lease/fencing. |
| `ClaimResult` | Result envelope for a worker claim. | Compatibility DTO; retire with Worker API. |
| `HeartbeatResult` | Result envelope for worker heartbeat. | Compatibility DTO; retire with Worker API. |
| `CompleteResult` | Result envelope for worker completion. | Compatibility DTO; retire with Worker API. |

### Distributed control plane

| Model | Current responsibility | Main architectural issue |
|---|---|---|
| `Node` | Runtime capacity, provider, labels/capabilities/tools, capacity, drain state, and heartbeat/expiry. | Retain as the owner of local effects. |
| `AgentSession` | One agent execution instance, including task, terminal, phase, attempt, status, heartbeat, outcome, and error. | Currently execution-shaped. Redefine as FleetDB-canonical continuity and move attempt state to Execution/ExecutionAttempt/TaskRun. |
| `TerminalSession` | PTY transport/status/stream/transcript attachment. | Retain strictly as transport; attach to canonical session/run. |
| `Artifact` | Typed artifact reference/content metadata owned by agent/session/task/run-like resources. | Retain; add Execution, ExecutionAttempt, TranscriptEvent, approval, checkpoint, and SDLC links. |
| `AgentLease` | Session execution lease with fencing. | Migrate to ExecutionAttempt/AgentDeployment lease or generic Lease resource typing. |
| `Lease` | Generic resource ownership lease. | Retain and prefer; add target resource kinds for RuntimeDeployment, AgentDeployment, ExecutionAttempt, and TaskRun. |
| `AgentOwnershipLease` | Ownership of an Agent by a Node/controller. | Convert to AgentDeployment or RuntimeDeployment instance lease; identity itself is not ownable runtime state. |
| `AgentCommand` | Desired command for an Agent/runtime. | Split target into AgentDeploymentCommand, RuntimeDeploymentCommand, or ExecutionCommand. |
| `AgentInboxMessage` | Addressed asynchronous message with delivery state. | Migrate to AgentMessage/Delegation while preserving delivery/audit fields. |

### Driver, trigger, service, and run platform

| Model | Current responsibility | Main architectural issue |
|---|---|---|
| `Driver` | Named executable behavior package, owner, active version, status, trust, metadata. | Retain as the initial HarnessArtifact registration form, not Agent identity. |
| `DriverVersion` | Immutable source/bundle version, digests, runtime, manifest, build/validation state. | Retain as the initial HarnessArtifactRevision form; reference from AgentRevision or WorkflowRevision. |
| `WorkerProfile` | Role/backend/runtime policy/repo/priority/concurrency/capability defaults for task workers. | Rename/refocus as ExecutionProfile; remove identity and organizational authority. |
| `AgentService` | Agent-like behavior plus kind, Role or DriverVersion, desired state, profile, schedule/events/triggers, placement, instance count, lease, restart, permissions, budget, state, and soft delete. | Split into Agent/Revision, implementation ref, subscription, optional AgentDeployment, shared RuntimeDeployment, and policy. |
| `TriggerBinding` | Source route/filter/schedule, DriverVersion target, optional AgentService target, concurrency/retry/idempotency/auth, webhook secret, permissions, enabled state. | Evolve to subscription/routing. Remove identity, behavior ownership, secret, and authority. |
| `TriggerEvent` | Normalized immutable occurrence with source/event/subject/payload, provenance, hop depth, and timestamps. | Retain; align envelope with CloudEvents-style source/id/type/subject fields. |
| `TriggerDelivery` | One event-to-binding fan-out attempt, concurrency subject, status/rejection, run, retry, error. | Retain; point to AgentSubscription/Workflow trigger and ExecutionIntent. |
| `ActionLedger` | Idempotent external/deterministic action state and outcome. | Retain; link policy/approval/effect evidence. |
| `DriverRun` | Bounded workflow execution with source, binding/service attribution, subject concurrency, parent/await, node/lease/fencing, payload/output, status, summary, and error. | Back canonical Execution plus ExecutionAttempt during migration; keep native workflow-run attribution without making Driver the product identity. |
| `DriverStep` | One workflow step with kind, TaskRun/ActionLedger/external refs and state. | Retain as implementation detail linked to workflow Execution and native framework provenance. |
| `TaskRun` | Finite task execution, runner/version/profile, node/lease/fencing, placements, input, usage/cost, logs/artifacts, status, and error. | Retain as canonical specialized task effect unit; link to parent Execution and current ExecutionAttempt. |
| `TaskRunCompletion` | Immutable fenced terminal completion envelope, task close intent, usage, artifacts, and errors. | Retain; this remains the atomic task completion authority. |
| `TaskRunLogEntry` | Ordered fenced stdout/stderr-style task log entry. | Retain; normalize into TranscriptEvent while preserving raw log provenance. |
| `AwaitInstance` | Durable wait condition and its matched/timeout/resume state. | Retain as orchestration internal linked to workflow Execution/Attempt. |
| `TaskRunEvent` | Durable task-run event for outbox delivery. | Retain as internal integration event, not user transcript. |
| `OutboxRecord` | Transactional/at-least-once delivery state for TaskRunEvent. | Retain as infrastructure record. |

`TriggerActorFilter`, `TaskRunPlacement`, and similar structs are embedded value
objects. They are part of the serialized contract but do not require independent
identity.

### Connectors and effect audit

| Model | Current responsibility | Main architectural issue |
|---|---|---|
| `Connector` | Provider boundary, inbound endpoint/signing secrets, sealed outbound credentials, status, rotation window. | Retain. Secret access moves behind mandatory approval and audited broker operations. |
| `ConnectorGrant` | Binding-scoped provider action/resource grant. | Retain grant semantics but attach issued grants to effective policy + Agent/Run, not TriggerBinding identity. |
| `ConnectorCallRecord` | Append-only record for granted, denied, stale, precondition-required, or failed egress calls. | Retain and link to Execution/Attempt, policy snapshot, approval, and SDLC refs. |

### Storage/recovery support

`ReconstructionResult` is an operation result used when rebuilding from journal
and snapshots. It is not a domain identity. Likewise validation enums, status
types, filters, and list responses are contract/value types, not records.

## Loom-side models outside FleetDB

### FleetDB wire mirrors

`loomcli/internal/domain` mirrors the FleetDB wire shapes for `Workspace`,
`Repo`, `Role`, `Agent`, `DaemonProfile`, `Node`, `AgentSession`,
`TerminalSession`, `Artifact`, lease/command/inbox records, `Driver`,
`DriverVersion`, `WorkerProfile`, `AgentService`, trigger records, `DriverRun`,
`DriverStep`, `TaskRun`, `TaskRunPlacement`, `TaskRunLogEntry`, `AwaitInstance`,
connectors, `TaskRunEvent`, and `OutboxRecord`.

These are not separate durable concepts. During migration, FleetDB OpenAPI and
generated or validated Loom clients must change together. Hand-maintained tag
differences are contract bugs, not a reason to keep two domain definitions.

`PlatformEvent` and `PlatformEventsPage` are Loom client journal envelopes.
They remain API DTOs.

### Filesystem session store

The Loom session subsystem currently persists:

| Model | Current responsibility | Target disposition |
|---|---|---|
| `SessionRecord` | Index entry in `sessions/index.jsonl` with agent/backend/task/status/timing/transcript/diff/usage metadata. | Import to AgentSession + Execution/Attempt + Artifact indexes; retain a compatibility reader temporarily. |
| `SessionMetadata` | Per-session metadata file embedding SessionRecord. | Import fields, then retire as authority. |
| `TranscriptEntry` | Ordered local transcript JSONL entry with role/type/content and timing/provider data. | Convert to canonical TranscriptEvent. |
| `Session` | In-process handle around metadata and store. | Runtime wrapper only; adapt to FleetDB session APIs. |
| `transcript.Event` | Normalized event emitted by provider transcript parsers. | Use as an adapter input to TranscriptEvent, not a second canonical envelope. |
| `Line`, `UserMessage`, `AssistantMessage`, `ContentBlock`, `ToolInput` | Provider/parser content values. | Map to versioned TranscriptEvent content parts. |
| `TokenUsage`, `DiffStats`, `DiffStatsMetadata` | Usage and diff summary values. | Move to run usage and Artifact metadata; retain compatibility parsing. |

`CreateOptions`, `FinalizeOptions`, `Filter`, `Store`, and event-store `Store`
are APIs/runtime helpers, not records. OpenCode export structs (`ExportSession`,
`SessionInfo`, `ExportMessage`, `MessageInfo`, `Time`, `Tokens`, `Cache`, `Part`,
`ToolState`, `ToolStateMetadata`, `ToolFileInfo`) are provider adapter shapes and
must remain outside the canonical schema.

### Stack and worktree lineage

| Model | Current responsibility | Target disposition |
|---|---|---|
| `stacklineage.Stack` | Durable local stack identity, repository, root base, and lifecycle. | Preserve as a specialized projection linked to SDLC refs; migrate to FleetDB if it must coordinate across Nodes. |
| `stacklineage.Node` | Task-to-branch/base/parent/output relationship within a stack. | Map to SDLCObjectRef and SDLCRelationship while retaining stack-specific fields. |
| `driver.TaskWorktree` | Resolved local path/repo/cleanup context. | Runtime value only; report placement on TaskRun. |
| `driver.TaskLineage` | Stack/base/output branch carrier in task input. | Runtime projection; canonical lineage lives in SDLC relationships/stack projection. |

### Trigger runtime values

`InternalEvent`, `AwaitDispatchEvent`, `AwaitMatchRecord`, `SubjectInputs`, and
sweep/emit result structs are routing/runtime values. Cursor files such as the
issue-journal bridge cursor are local controller checkpoints. They should move
to a leased durable subscription checkpoint only when multiple controllers can
own the same source; they are not Agents or SDLC objects.

## Integrated external harness data models

### Flue at the exact Loom pin

Flue is not part of the FleetDB schema, but its native model is part of the
current execution boundary and must be accounted for. The source is the local
`withastro/flue` checkout at
`492bf47b9f3d6c379d00471523987b8fe9511f7d`, matching
`internal/workflows/FLUE_COMMIT`.

#### Authoring and runtime hierarchy

| Flue concept | Flue responsibility | Canonical Loom mapping |
|---|---|---|
| `AgentProfile` | Reusable model/instructions/tools/skills/actions/subagents/thinking/compaction/durability configuration. | Native artifact content projected to ComponentRefs; not Loom Role or Agent. |
| `AgentDefinition` | Initializer returned by `defineAgent(...)`. | AgentImplementationRef definition key inside HarnessArtifactRevision. |
| agent module | Addressable `agents/<name>.ts` definition and static description/transports. | Normalized artifact manifest entry. |
| `AgentInstance` | Continuing native instance selected by module name + application-provided ID. | RuntimeSessionBinding context; not automatically a Loom Agent. |
| Harness | Runtime initialized environment for an AgentInstance or WorkflowRun. | Native runtime scope on ExecutionAttempt/RuntimeSessionBinding. |
| Session | Named continuing message/context tree inside a Harness. | Bound private state beneath FleetDB AgentSession. |
| Operation | `prompt`, `skill`, `task`, `shell`, or `compact` activity. | TranscriptEvent span inside a canonical Execution. |
| Turn | One model round trip and tool activity. | Model/tool TranscriptEvents inside an Execution. |
| `WorkflowDefinition` | One finite Action bound to one AgentDefinition. | WorkflowRevision implementation/manifest ref. |
| `WorkflowRun` / `RunRecord` | Finite workflow invocation with `runId`, input/result/error/status. | `Execution(kind=workflow)` plus native run ref. |

#### Persistence, durability, and evidence

| Flue model | Responsibility | Canonical Loom treatment |
|---|---|---|
| `SessionData` / `SessionStore` | Versioned session tree, message/compaction entries, child-session refs, metadata, provider affinity. | RuntimeCheckpoint/private adapter storage; FleetDB AgentSession/TranscriptEvent remain canonical. |
| `AgentSubmission` / `AgentSubmissionStore` | Ordered direct/dispatch admission, status, attempts, leases, recovery, deletion coordination. | Interaction Execution + ExecutionAttempt; store private journal fields in adapter namespace. |
| `AgentTurnJournal` | Recovery phase, operation/turn IDs, stream/checkpoint state, commit evidence. | RuntimeCheckpoint plus normalized attempt/evidence events. |
| `AgentAttemptMarker` | Durable proof a native submission attempt may still be running. | Native attempt evidence correlated to FleetDB ExecutionAttempt. |
| `DispatchReceipt` | `dispatchId` and accepted time; explicitly not a workflow run. | Native ref on ExecutionIntent/Execution. |
| `RunStore` | Flue WorkflowRun creation/finalization/listing. | Adapter storage/projection for workflow Executions; never universal history. |
| `FlueEvent` | Versioned runtime event with run/instance/dispatch/submission/session/operation/turn/task correlations. | Source event normalized to TranscriptEvent with native version/ID/cursor provenance. |
| `EventStreamStore` | Durable native append/read/subscribe stream with Flue offsets. | Adapter checkpoint/source stream; FleetDB assigns canonical stream sequence. |
| `AgentManifestEntry` | Built agent name/description/transports/defined flag. | HarnessArtifactRevision normalized manifest entry. |

Flue `ActionDefinition`, tools, skills, sandboxes, channel modules, provider
settings, and deployment target configuration are artifact/runtime components,
not independent Loom identities. Their normalized names, versions, and digests
become ComponentRefs when available.

Vercel eve, CLI, SDK, and future Loom-native harness inventories should be
added using the same three-way distinction: native authoring/runtime model,
private checkpoint semantics, and canonical FleetDB mapping.

## Current identity and lifecycle problems to remove

### Three sources appear as Agents

The Loom Agents API currently unions:

1. old `Agent` records as `supervised`;
2. `AgentService` records as `prompt` or `scripted`;
3. unattached TriggerBindings as `binding` agents.

This is a useful compatibility projection but an unsafe source of permanent
semantics: a routing rule without identity can appear to be an Agent, and two
different records can represent the same human concept.

### Prompt agents are distributed aggregates

Creating or editing a prompt agent coordinates Role, Driver/DriverVersion,
AgentService, TriggerBinding, connector grants, and generated configuration.
Desired state and binding enabled state can mirror each other. Partial failures
require startup healing. The target model may still use a transaction/saga, but
the aggregate root must be Agent + AgentRevision; subscriptions and deployments
are attached resources, not identity fragments.

### Historical attribution still depends on current routing

DriverRun now snapshots `AgentServiceID` and `TriggerBindingID`, which is good.
Some Loom history paths still enumerate current bindings to find DriverRuns.
Replacing or deleting a binding can therefore hide attributable history even
though the DriverRun retained an AgentService ID. Target history queries
canonical Agent/Execution identity directly and treat routes/native run IDs as
historical context only.

### Ready events approximate rather than own readiness

The task-ready bridge observes `status=open` from journal snapshots, while full
ready eligibility depends on current blockage, deferral, filters, priority, and
claim state. Its safe claim-and-decline behavior prevents the wrong task from
running, but it should be a wake-up signal for a canonical ready-frontier pull,
not an alternate queue.

### Session and run terms overlap

Control-plane AgentSession says "one execution instance"; filesystem Session
is conversation/execution history; DriverRun is orchestration; TaskRun is task
execution; TerminalSession is transport. Without a strict boundary, health,
usage, transcript, and outcome can attach to different IDs depending on runtime.

### Authorization is route-coupled

ConnectorGrant is scoped to TriggerBinding. This makes authority appear to come
from how work started. The same Agent entering through a terminal, delegation,
or another subscription should have the same policy; each run should receive
only the effect grants admitted for it.

### Cross-repository API drift is possible

Loom pins a FleetDB OpenAPI snapshot. The branch changes both repos independently.
Migration must version and gate the server/client contract together, with a
generated or deterministic compatibility check in CI.

### Flue is an external framework with its own runtime model

The local checkout at `/Users/tyson/codebase/code-agents/flue` is detached at
`492bf47b9f3d6c379d00471523987b8fe9511f7d`, exactly matching Loom's
`internal/workflows/FLUE_COMMIT` pin. Its canonical terminology is:

```text
AgentProfile -> AgentDefinition -> AgentModule -> AgentInstance
  -> Harness -> Session -> Operation -> Turn

Workflow -> WorkflowRun
```

Flue explicitly reserves *run* for workflows. Direct prompts and dispatched
inputs are durable submissions/operations in continuing sessions. It also owns
SessionData, AgentSubmissionStore, RunStore, EventStreamStore, FlueEvent,
subagent profiles/child sessions, framework build output, and channel ingress
semantics.

The target must not copy these names into universal Loom semantics. A versioned
Flue HarnessAdapter maps them into FleetDB-canonical AgentSession, Execution,
ExecutionAttempt, TranscriptEvent, and RuntimeCheckpoint records. The same
canonical types must also support Codex/Claude CLIs, SDKs, Vercel eve, and a
future Loom-native harness.

## Target durable model catalog

### New or materially revised first-class records

| Target model | Minimum identity and links | Purpose |
|---|---|---|
| `Organization` | `organization_id`, name/status, identity-provider and policy-root refs | Highest tenant and governance boundary. |
| `PrincipalRef` | `principal_ref_id`, organization, provider, external subject, kind, display metadata | Stable reference to a human, service, or group for ownership, grants, and approvals. |
| `OrganizationMembership` | organization, PrincipalRef, membership/authority revision, validity | Decision-time organization membership and approval authority evidence. |
| `Role` | `workspace_key`, stable `role_id`, name/status | Durable organizational job identity. |
| `RoleRevision` | `role_revision_id`, `role_id`, ordinal/digest, responsibility/outcomes/default refs | Immutable Role history. |
| `PolicySet` | `policy_set_id`, scope/owner, name/status | Stable policy identity. |
| `PolicyRevision` | `policy_revision_id`, `policy_set_id`, schema version, rules, digest | Immutable enforceable policy. |
| `EffectivePolicySnapshot` | digest, input revision refs, expanded critical rules | Reproducible policy pinned to an Execution; may be content-addressed/deduplicated. |
| `Agent` | `agent_id`, workspace, display/name/status, created/archived provenance | Stable responsible actor identity. |
| `AgentRevision` | `agent_revision_id`, Agent, RoleRevision, responsibility/persona, policy refs, AgentImplementationRef, ComponentRefs, digest | Immutable governed definition used by Executions without copying a harness schema. |
| `HarnessAdapter` | adapter ID/kind/name/status | Stable integration identity for a framework, CLI, SDK, or Loom-native harness. |
| `HarnessAdapterRevision` | adapter revision, supported native versions, capabilities, mapping schema/code digest, compatibility evidence | Immutable integration and normalization implementation pinned by every ExecutionAttempt. |
| `HarnessArtifact` | artifact ID, owner/name/status, active revision | Stable registration identity for executable harness packages. |
| `HarnessArtifactRevision` | artifact revision, harness version, compatible adapter revision, source/bundle digests, trust/validation, normalized manifest, native manifest ref | Immutable registered build; existing DriverVersion is its first storage form. |
| `AgentImplementationRef` | AgentRevision, artifact revision, native definition kind/key/entrypoint, adapter config digest | Binds governed Agent identity to an immutable harness implementation. |
| `ComponentRef` | kind/name/version/digest/provenance | Normalized analytics/reproducibility projection for model, prompt/instructions, skill, tool, harness, adapter, or sandbox. |
| `Workflow` | workflow ID, owner/name/status | Stable registered finite coordination identity. |
| `WorkflowRevision` | Workflow, artifact revision/definition key, input/output/capability projection, digest | Immutable registration without copying a framework's private graph. |
| `AgentSubscription` | subscription ID, target Agent/Workflow, source/route/filter/schedule, concurrency/retry/idempotency, enabled | Activation/routing without identity or authority. |
| `RuntimeDeployment` | deployment ID, artifact revision, desired/observed instances, placement/rollout/restart/health | Deploys a harness artifact that may contain multiple Agents/Workflows. |
| `AgentDeployment` | deployment ID, Agent/revision channel, RuntimeDeployment/direct placement, desired/observed dedicated instance | Optional resident/dedicated Agent lifecycle, not the universal runtime unit. |
| `ExecutionIntent` | intent ID, target, source/SDLC refs, semantic kind, requested capabilities/outcome, dedup/concurrency, initiator/delegator, admission | Durable pre-execution request. |
| `Execution` | execution ID, intent, semantic kind, AgentRevision/WorkflowRevision, policy snapshot, session/parent/delegation, implementation, state/outcome/usage | Harness-neutral accountable business unit. |
| `ExecutionAttempt` | attempt ID/ordinal, Execution, HarnessAdapterRevision/artifact/deployment/Node, lease/fencing, native IDs/type, checkpoint, observed state/outcome | Runtime ownership, retry, and resume boundary. |
| `AgentSession` | session ID, Agent, channel/context/source refs, lifecycle/retention | FleetDB-canonical continuity containing zero or more Executions. |
| `RuntimeSessionBinding` | AgentSession, adapter/artifact/deployment, native instance/harness/session/thread IDs, cursor | Maps canonical continuity to harness-native context. |
| `RuntimeCheckpoint` | checkpoint ID, adapter/schema version, session/execution/attempt, encrypted payload ref/digest, retention | Private harness recovery state under a FleetDB-owned envelope. |
| `TranscriptEvent` | event ID, ordered canonical stream/sequence, session/execution/attempt lineage, normalized type/data, native provenance, redaction | FleetDB-canonical append-only evidence envelope. |
| `Delegation` | delegation ID, from/to Agents/Executions, requested outcome, constraints, budget/deadline, status/result | Explicit responsibility transfer across Loom governance boundaries. |
| `AgentMessage` | message ID, sender/recipient refs, session/execution/delegation, content ref, delivery state | Addressed communication where delivery must be durable. |
| `ApprovalRequest` | approval request ID, Execution, action class, exact subject/version, required policy, state/expiry | Durable protected-action gate. |
| `ApprovalDecision` | decision ID, request, approver, decision, reason/conditions/time | Immutable human decision. |
| `SDLCObjectRef` | provider/scope/kind/external ID unique key plus Loom ID | Stable normalized reference to internal or external SDLC object. |
| `SDLCProjection` | object ref, normalized fields, source version, observed/fresh-until | Cached routing/policy/UI view, not provider authority. |
| `SDLCRelationship` | from/to refs, type, provenance, validity | Cross-system SDLC graph edge. |
| `AgentEvaluation` | evaluation ID, Execution/revision/component refs, evaluator/method/version, metrics/findings | Immutable feedback for analytics and controlled improvement. |
| `ExecutionProfile` | profile ID, runner/provider/sandbox/placement/resource defaults | Reusable runtime template, replacing WorkerProfile's identity-like semantics. |

There is intentionally no `AgentMemory` model.

### Retained implementation and infrastructure records

`Driver`, `DriverVersion`, `DriverStep`, `TaskRun`, `TaskRunCompletion`,
`TaskRunLogEntry`, `TaskRunEvent`, `OutboxRecord`, `AwaitInstance`,
`ActionLedger`, `Node`, generic `Lease`, `TerminalSession`, `Artifact`,
`Connector`, `ConnectorCallRecord`, `Issue`, `Dependency`, `Comment`, `Event`,
`Snapshot`, and `IdempotencyRecord` remain. Their foreign keys and owner/resource
enums expand to target IDs. Driver/DriverVersion act as compatibility storage
and API projections for HarnessArtifact/HarnessArtifactRevision until renamed or
replaced.

## Source-to-target migration matrix

| Current source | Target | Transformation |
|---|---|---|
| `Role` | Role + RoleRevision + referenced PolicySet/Revision | Keep stable identity/name. Move description/responsibility and recommended behavior to RoleRevision. Convert tool, read-only, path/repo/task eligibility, priority, concurrency, and budget fields to policy rules while preserving compatibility projections. |
| `Workspace` | Workspace under Organization | Add `organization_id`; existing installations receive one deterministic default Organization before workspace backfill. |
| old `Agent` | Agent + AgentRevision + AgentImplementationRef | Create stable Agent ID. Resolve RoleRevision and adapter implementation. Convert backend/fallback to adapter configuration/component refs; convert task/repo/filter/budget/concurrency/auto to policy. |
| old `Agent.Mode`, `DesiredState`, runtime `State` | optional AgentDeployment | Create deployment for service/interactive/supervised records; do not put observed runtime on Agent. Ephemeral/event-only agents get no deployment. |
| old `Agent.Parent` | explicit relationship/delegation defaults | Preserve organizational relationship if meaningful; never infer active run delegation from a static parent alone. |
| `AgentService` | Agent + AgentRevision + AgentImplementationRef | One stable Agent per service identity. Role-backed service resolves RoleRevision; Driver-backed service resolves HarnessArtifactRevision/definition. Preserve metadata, creator, archive time, and legacy map. |
| AgentService kinds `lead`, `orchestrator`, `always_on`, persistent `support/on_call` | AgentDeployment plus RuntimeDeployment/direct adapter placement | Move dedicated desired state and instance policy to AgentDeployment; move shared artifact process/endpoint lifecycle to RuntimeDeployment. Validate ambiguous rows. |
| AgentService kinds `event`, `cron`, `scheduled` | AgentSubscription; optional RuntimeDeployment, usually no dedicated AgentDeployment | Move activation to subscriptions. The framework artifact may still need a shared runtime deployment. |
| AgentService permissions/budget | PolicySet/Revision | Translate without widening; unknown strings become deny/manual-review, not allow. |
| `WorkerProfile` | ExecutionProfile + policy references where required | Keep runner/backend/runtime/placement/resource defaults; move work eligibility and authority to policy. |
| `Driver`/`DriverVersion` | HarnessArtifact/HarnessArtifactRevision compatibility projection | Preserve IDs, immutable versions, source/bundle digests, validation/trust/provenance. Add harness/adapter version and normalized manifest/component projection. |
| built Flue `dist` + `FLUE_COMMIT` | HarnessArtifactRevision | Register exact Flue framework pin, adapter version, bundle digest, manifest entries, native manifest ref, and component projections. Never infer from current Flue HEAD. |
| CLI/SDK backend config | HarnessArtifactRevision or versioned adapter profile | Snapshot CLI/SDK version, model/config/component digests and capability manifest without treating a process as an artifact revision. |
| `TriggerBinding` | AgentSubscription or Workflow trigger | Preserve source, route, filter, schedule, retry, concurrency, idempotency, enabled state. Target Agent/Workflow replaces Driver/AgentService identity coupling. Move secrets to Connector and permissions to policy grants. |
| unattached binding shown as Agent | classified Agent + subscription, Workflow trigger, or automation | Do not blindly mint an Agent. Classify by responsibility metadata; ambiguous records enter migration review. |
| `TriggerEvent` | TriggerEvent | Preserve IDs/payload/provenance; add normalized envelope version and SDLC refs. |
| `TriggerDelivery` | delivery to subscription + ExecutionIntent | Preserve attempts/status/rejections/native DriverRun link; backfill intent and canonical Execution. |
| `DriverRun` | Execution + ExecutionAttempt + native workflow ref | Classify semantic kind from binding/driver behavior, never from adapter name. Preserve run ID as native/compatibility ref and its parent/await/output history. |
| `DriverStep` | implementation step linked to workflow Execution | Preserve order/kind/status and TaskRun/ActionLedger/external refs as native orchestration detail/transcript evidence. |
| `TaskRun` | TaskRun child of Execution/ExecutionAttempt | Add canonical parent IDs and policy snapshot. Preserve TaskRun ID, lease/fencing, placement, usage, and completion authority. |
| control-plane task-kind `AgentSession` | Execution/Attempt/TaskRun plus optional AgentSession | Move attempt/phase/status/error to Execution/Attempt. Retain AgentSession only for real continuity. |
| other control-plane `AgentSession` | canonical AgentSession + linked Executions | Preserve terminal/parent/summary metadata; separate continuity from execution outcome. |
| filesystem `SessionRecord`/metadata | AgentSession + Execution/Attempt + RuntimeSessionBinding + Artifact | Import deterministic canonical IDs, backend/native refs, status/times/task/diff/usage, and source checksum. |
| filesystem `TranscriptEntry`/parsed events | TranscriptEvent | Convert sequence/content/tool/usage with adapter/mapping version and native provenance; preserve raw transcript as protected Artifact where allowed. |
| Flue AgentDefinition/module | AgentImplementationRef or WorkflowRevision implementation ref | Reference immutable artifact revision and module/definition key; do not copy AgentRuntimeConfig as Loom's schema. |
| Flue AgentInstance/Harness/Session | RuntimeSessionBinding + RuntimeCheckpoint | Map instance/harness/session/storage identifiers under FleetDB AgentSession; store private SessionData/checkpoint state by adapter schema version. |
| Flue direct/dispatch submission and Operation | interaction Execution + ExecutionAttempt + TranscriptEvents | FleetDB assigns canonical IDs; retain submission/dispatch/operation/turn IDs as native refs. Never create a WorkflowRun projection. |
| Flue WorkflowRun | workflow Execution + ExecutionAttempt | Retain Flue runId/workflowName/status/input/result as native provenance while FleetDB owns canonical lifecycle. |
| FlueEvent/EventStreamStore | TranscriptEvent + native cursor/provenance + optional RuntimeCheckpoint | Map stable fields idempotently; FleetDB assigns canonical sequence. Record loss/unmapped payload explicitly. |
| Flue AgentSubmissionStore/SessionData/RunStore | adapter-private checkpoint/storage namespace | Back with FleetDB-controlled storage and run Flue contract tests; never expose these types as canonical Loom APIs. |
| Codex/Claude CLI session/process/transcript | AgentSession + RuntimeSessionBinding + Execution/Attempt + TranscriptEvent | Map resume/session/process IDs and stream events; FleetDB owns status/history and artifacts. |
| SDK thread/response/trace | RuntimeSessionBinding + Execution/Attempt + TranscriptEvent | Preserve provider-native refs while normalizing accountability, evidence, usage, and policy. |
| `TerminalSession` | TerminalSession | Link to canonical AgentSession and current ExecutionAttempt; preserve PTY/stream state. TranscriptRef becomes provenance, not history authority. |
| `AgentLease` | ExecutionAttempt/AgentDeployment Lease | Preserve fencing and expiry; target the runtime-owned resource explicitly. |
| `AgentOwnershipLease` | AgentDeployment or RuntimeDeployment instance Lease | Do not lease Agent identity. |
| `AgentCommand` | RuntimeDeploymentCommand, AgentDeploymentCommand, or ExecutionCommand | Classify target/action; preserve idempotency/status/audit. |
| `AgentInboxMessage` | AgentMessage or Delegation | Preserve sender/recipient/body/reference/delivery. Structured work requests become Delegation. |
| `ConnectorGrant` | policy-issued CapabilityGrant | Preserve action/resource/revocation; attach to policy/Execution/subscription provenance. Binding-only grants remain deny-by-default until mapped. |
| `ConnectorCallRecord` | ConnectorCallRecord | Add Execution/Attempt, policy, approval, and SDLC subject refs. Never discard denied calls. |
| `Artifact` | Artifact | Backfill Execution/Attempt/Workflow/SDLC ownership and retain hashes/URIs. |
| stacklineage Stack/Node | stack projection + SDLC refs/relationships | Preserve IDs/root base/branch lineage; link branches/tasks/PRs through generic refs. |
| legacy `Worker` | Node + TaskRun history where derivable | Preserve audit snapshot, then stop new claims and retire after no active leases. |

## Migration mechanics

### Stable IDs and the legacy identity map

Introduce an append-only `LegacyIdentityMap` (or equivalent migration namespace)
with:

```text
workspace_key
source_system       # fleetdb or loom-local
source_kind         # agent, agent_service, binding, session, driver_run, ...
source_id
target_kind
target_id
migration_version
source_digest
classified_by       # deterministic rule or reviewed actor
created_at
```

The `(workspace, source_kind, source_id, migration_version)` key is unique.
Backfills are idempotent. Display names are never used as the only identity key.
Where an existing immutable ID already has safe semantics, retain it or store it
as the implementation ID; do not churn IDs for aesthetics.

FleetDB allocates `execution_id`, `attempt_id`, and canonical `session_id`
before invoking an adapter. The adapter receives those IDs as idempotency and
correlation inputs. Native mappings are unique within their real scope, for
example `(adapter_revision_id, runtime_deployment_id, native_kind, native_id)`;
native IDs are never assumed globally unique and never replace canonical IDs.

### Revision construction

For each source aggregate:

1. canonicalize ordered fields and explicit defaults;
2. resolve Role and Policy revision references;
3. register or resolve HarnessArtifactRevision and AgentImplementationRef;
4. ask the adapter for normalized ComponentRefs and capability/manifest projection;
5. compute a deterministic AgentRevision or WorkflowRevision digest;
6. reuse an existing matching revision or create the next immutable ordinal;
7. record source IDs/digests and adapter/mapping versions as provenance.

An unresolvable artifact, implementation key, DriverVersion, Role, adapter, or
policy reference does not silently fall back. The record becomes
`migration_blocked` or is imported disabled with a diagnostic. Missing prompt,
skill, tool, or model component projections do not fabricate values; they are
marked unavailable while the immutable artifact remains authoritative.

### Lifecycle/status mapping

Status backfill separates business Execution state, runtime-attempt state, and
session continuity instead of copying one legacy enum into all three:

| Source state | Canonical mapping |
|---|---|
| DriverRun `queued` | Execution `queued`; no active attempt unless a claim already exists |
| DriverRun `running` | Execution `running` plus active ExecutionAttempt with Node/lease/fence |
| DriverRun `suspended_awaiting_event` | Execution `suspended`; await/checkpoint and attempt ownership preserved |
| DriverRun `completed` | Execution `completed` with result/artifact/usage refs |
| DriverRun `needs_review` | Execution `completed` with disposition `needs_review`; SDLC review state remains separate |
| DriverRun `failed` / `cancelled` | Execution corresponding terminal state and normalized error/reason |
| task-kind AgentSession queued/leased/starting/running | Execution/ExecutionAttempt state; not canonical session lifecycle |
| task-kind AgentSession completed/failed/cancelled/expired | Execution/Attempt outcome; retain AgentSession only if continuity existed |
| Flue submission queued/running/settled | Native observed attempt state reconciled into one interaction Execution; terminal result comes from mapped native evidence |
| Flue WorkflowRun active/completed/errored | Native state reconciled into workflow Execution and attempt |
| CLI process start/exit/retry | ExecutionAttempt lifecycle; a retry does not create another Execution unless it is a new admitted request |

Unknown or contradictory source state is imported with a reconciliation
diagnostic and cannot be silently promoted to success.

### External object identity

Upsert external refs on `(workspace, provider, provider_scope, kind,
external_id)`. Projection updates require a monotonic provider version when
available. Historical events and transcripts reference the stable object ID,
not a copied URL. Provider deletion marks the projection tombstoned but does not
erase Loom evidence subject to retention policy.

### Execution and transcript ordering

- Preserve source run/session/submission/process/trace IDs in LegacyIdentityMap
  and native provenance fields; never use them as canonical primary IDs.
- Assign TranscriptEvent sequence by source-native sequence where trustworthy;
  otherwise use deterministic observed order and record `ordering_quality`.
- Deduplicate imports by `(adapter, native stream/ref, native event identity or
  source digest/sequence, mapping version)`.
- Create one Execution for the admitted business unit and separate
  ExecutionAttempts for process/runtime retries; do not turn every native
  Operation, Turn, or tool call into an Execution.
- Do not synthesize tool success when only a call is present.
- Reconcile usage/status totals against source summaries and retain
  discrepancies as import diagnostics rather than rewriting evidence.

### Policy migration fails closed

Every legacy tool, permission, repo, path, task, priority, budget, model, secret,
and connector field must be classified as descriptive, executable default, or
enforceable rule. Unknown/invalid permissions produce deny/manual review.
Policy migration may preserve an existing allow but cannot infer a broader one.
The seven protected action classes receive mandatory approval rules even if a
legacy record currently allows them without approval.

## Phased migration

Each phase has a read flag, idempotent backfill, reconciliation report, and
rollback to the previous read path. A rollback stops new writes to the phase's
target records but never deletes successfully imported history.

### Phase 0 — freeze semantics and instrument current aggregates

Deliver:

- accept ADR-0001 and this glossary;
- assign schema owners and versioned API compatibility policy;
- add metrics for `/agents` source kind, aggregate healing, binding-dependent
  history, ready-event declines, and session/execution/native ID relationships;
- inventory and export current counts/checksums by workspace and record kind;
- inventory every runtime/backend's session, process/run, transcript, resume,
  artifact, and usage formats;
- record exact harness/CLI/SDK pins, including Flue `492bf47...`;
- stop adding identity, permission, or generic execution semantics to
  AgentService, TriggerBinding, or DriverRun.

Exit gate: every current source can be counted, exported, and reconciled; no
unknown writer exists; every supported runtime has a named adapter owner.

### Phase 1 — add target records without changing reads

Add FleetDB schemas/APIs/indexes for Organization, PrincipalRef and membership,
RoleRevision, PolicySet/Revision, EffectivePolicySnapshot, canonical
Agent/AgentRevision, HarnessAdapter/Revision, HarnessArtifact/Revision,
AgentImplementationRef, ComponentRef, Workflow/Revision, AgentSubscription,
RuntimeDeployment, AgentDeployment, ExecutionIntent, Execution,
ExecutionAttempt, canonical AgentSession, RuntimeSessionBinding,
RuntimeCheckpoint, TranscriptEvent, AgentMessage, Delegation, approvals, SDLC
refs/projections/relationships, AgentEvaluation, and ExecutionProfile.

Also expand Artifact, Lease, connector audit, and ownership enums. Use set-based
PostgreSQL migrations and equivalent Redis/archive behavior.

Exit gate: CRUD/validation/storage parity across supported backends; OpenAPI and
Loom client contract tests pass; canonical enums contain no harness names; no
product read depends on new records.

### Phase 2 — register adapters and immutable implementation artifacts

Implement the versioned adapter contract and capability discovery. Register:

1. the exact pinned Flue framework version and a Flue adapter version;
2. existing built Flue artifacts from DriverVersion rows;
3. Codex and Claude CLI adapter profiles/version probes;
4. normalized manifest and ComponentRef projections;
5. adapter compatibility, contract-test, and unsupported-capability results.

The Flue adapter must map AgentDefinition/Instance, Harness, Session,
submission/Operation, WorkflowRun, FlueEvent, and persistence/checkpoint
semantics without exposing them as canonical Loom schemas. A FleetDB-controlled
Flue persistence backend runs Flue's own adapter contract suites.

Exit gate: every active DriverVersion/backend resolves to one supported
HarnessArtifactRevision/adapter profile or is explicitly disabled with a
diagnostic; artifact and native manifest digests reconcile.

### Phase 3 — backfill identity, revisions, policy, and deployment bindings

Run deterministic migrations in dependency order:

1. default/declared Organizations, PrincipalRefs, then Workspace/Repo refs;
2. Role identities and initial RoleRevisions;
3. PolicySets/Revisions translated from organization, workspace, Role, Agent,
   AgentService, WorkerProfile, connector, and runtime rules;
4. Agents/AgentRevisions plus AgentImplementationRefs from old Agent and
   AgentService;
5. Workflow/WorkflowRevision registrations for finite implementations;
6. AgentSubscriptions, RuntimeDeployments, and optional AgentDeployments;
7. LegacyIdentityMap and per-record diagnostics.

Do not automatically convert an unattached binding to Agent unless its metadata
establishes responsibility. Queue ambiguous binding/script records for review.

Exit gate: 100% of active old Agent and AgentService rows map to exactly one
Agent; every active target Agent has an AgentRevision and effective baseline
policy plus a valid implementation ref; shared framework deployment is not
duplicated once per Agent; ambiguous rows are disabled/reviewed, not omitted.

### Phase 4 — dual-write authoring through canonical aggregates

Make Agent + AgentRevision the write-side aggregate root. Compatibility writers
materialize old AgentService/Role/Binding fields only while old readers exist.
Harness artifacts, Workflows, subscriptions, RuntimeDeployments,
AgentDeployments, and policies have their own endpoints and transactions/sagas.
Reconciliation compares semantic projections, not raw JSON or framework-private
payloads.

Prevent new binding-only UI agents. Existing ones remain visible through a
legacy adapter until classified.

Exit gate: create/update/archive tests prove atomic or compensatable behavior;
startup healer reports zero unexplained repairs for a full release window.

### Phase 5 — unify ExecutionIntent, Execution, and ingress

Introduce ExecutionIntent for:

- ready-work scheduler pulls;
- TriggerEvent deliveries;
- manual and scheduled requests;
- terminal/framework/provider conversation inputs;
- Agent delegations.

Replace task-ready direct-dispatch semantics with wake -> canonical ready query
-> atomic claim -> intent. Preserve external-event direct routing. Create one
semantic Execution per admitted business unit and one or more
ExecutionAttempts through adapters. Stamp Agent/Workflow revision,
EffectivePolicySnapshot, implementation, and artifact revision.

Exit gate: replay/idempotency/concurrency tests cover both ingress classes;
ready-work order matches FleetDB; no TaskRun starts from an unclaimed task;
Flue submissions are interaction Executions rather than fake workflow runs;
rejected intents are auditable.

### Phase 6 — unify sessions, attempts, transcripts, and checkpoints

Create Execution/ExecutionAttempt projections over current DriverRun, TaskRun,
and process/session records, then dual-write canonical records. Query history by
Agent/Execution identity, never by current bindings or only native IDs.

Import filesystem sessions and transcripts. Update Flue, Codex CLI, Claude Code,
OpenCode, terminal, background, and SDK adapters to write canonical
AgentSession, RuntimeSessionBinding, ExecutionAttempt, and TranscriptEvent
records. Retain raw provider/framework data as protected artifacts or
RuntimeCheckpoints where policy permits.

Redefine AgentSession as continuity. Stop creating task-kind sessions merely to
represent TaskRun execution. Link TerminalSession to active ExecutionAttempt.
FleetDB assigns canonical transcript sequence; adapter replay is idempotent and
native mapping loss is explicit.

Exit gate: the same transcript API renders all supported runtime classes;
session/execution/attempt/task lineage and usage reconcile; removing a
subscription does not remove history; Flue/CLI private state is not the sole
history authority; local session files are no longer authoritative.

### Phase 7 — separate RuntimeDeployment and AgentDeployment supervision

Change framework/service reconciliation to RuntimeDeployment and dedicated
Agent reconciliation to AgentDeployment. Move artifact rollout, endpoint,
shared instances, placement, restart, drain, health, ownership leases, and
runtime commands out of Agent and AgentService.

Event/scheduled Agents prove they need no dedicated AgentDeployment while using
a shared Flue/eve RuntimeDeployment. Interactive leads prove terminal attachment
and restart through AgentDeployment. Direct one-shot CLI adapters prove they can
create attempts without a persistent RuntimeDeployment.

Exit gate: no supervisor reads old Agent desired state or AgentService desired
state; deployment/attempt leases fence stale owners; multi-definition framework
artifacts deploy once; stop/drain/restart paths retain canonical transcripts and
Execution attribution.

### Phase 8 — cut over product reads and protected effects

Switch UI/API to first-class Agents, Workflows, Executions, AgentSessions,
runtime artifacts/deployments, and optional AgentDeployments. Show adapter and
native IDs as implementation details. Keep compatibility Drivers/bindings in
advanced views.

At connector/tool effect boundaries, require valid policy grants and human
ApprovalDecisions for merge, production deploy, rollback, incident
communication, destructive infrastructure changes, secret access, and budget
escalation. Require execution-scoped broker credentials and provider
freshness/precondition checks; remove credentials/egress that permit harness
tools to bypass the broker.

Exit gate: all protected-action negative and stale-subject tests fail closed;
all product history reads use canonical IDs; Flue/CLI/SDK adapters demonstrate
the same policy/evidence behavior; old and new projections match for the agreed
compatibility window.

### Phase 9 — retire legacy authorities

Stop compatibility writes, then remove after archive/export verification:

- AgentService as identity;
- runtime/desired-state/policy fields on old Agent;
- WorkerProfile identity/eligibility semantics;
- TriggerBinding identity, embedded permissions, and direct secret ownership;
- binding-derived run-history lookup;
- task-kind AgentSession as an execution attempt;
- filesystem SessionRecord/transcript as active authority;
- DriverRun as the universal product execution/history identity;
- framework-native session/run/event formats as product API responses;
- legacy Worker claims;
- duplicate hand-maintained API shapes not guarded by generation/contract tests.

Retain read-only import adapters and LegacyIdentityMap for the published support
window. Tombstones, archived Agents, historical Executions, native mappings,
transcripts, decisions, and denied connector calls are not deleted by cleanup.

Exit gate: zero old writers for a full release window; restore drill from the
pre-cutover export succeeds; compatibility flags can be removed without data or
behavior change.

## Reconciliation and acceptance checks

The migration is complete only when these checks hold per workspace:

| Check | Required result |
|---|---|
| Active identity cardinality | Every old Agent/AgentService maps once; no unintended Agent duplicates. |
| Revision coverage | Every Execution pins exactly one AgentRevision or WorkflowRevision plus immutable implementation/artifact refs. |
| Adapter coverage | Every ExecutionAttempt names a supported adapter/version; unsupported native capabilities fail explicitly. |
| Policy coverage | Every admitted Execution has an EffectivePolicySnapshot digest; unknown legacy permissions are denied/reviewed. |
| Deployment separation | Shared harness artifacts use RuntimeDeployment; event/schedule Agents need no dedicated AgentDeployment; resident Agents reconcile only from AgentDeployment. |
| Ready-work parity | Selected task IDs and ordering match canonical FleetDB ready/claim behavior. |
| Execution semantics | Flue direct/dispatch inputs map to interaction Executions, Flue WorkflowRuns to workflow Executions, and CLI/SDK activity maps without harness-named kinds. |
| History preservation | Archived Agents and deleted subscriptions still return Executions/transcripts through canonical Agent ID. |
| Transcript ordering | Canonical sequence is unique/monotonic; native replay is idempotent; mapping diagnostics explain gaps, duplicates, and loss. |
| Session authority | FleetDB AgentSession is authoritative; native sessions/checkpoints are bound private state, not product history. |
| Usage reconciliation | Imported Execution/session totals equal native sources or carry explicit discrepancy records. |
| Task correctness | Only the current fenced TaskRun completion changes task terminal state. |
| Approval enforcement | All seven protected classes reject missing, expired, stale, mismatched, or unauthorized decisions. |
| Effect broker | Harness tools cannot perform protected effects without Execution-scoped grants and required approval. |
| Connector audit | Granted and denied effects link Execution/Attempt, policy, approval when required, and exact SDLC subject. |
| External authority | Projection lag/staleness is visible; no cached projection is used as destructive-action truth. |
| Cross-session memory | No implicit read/write path exists; later evidence retrieval is explicit and audited. |
| API compatibility | FleetDB OpenAPI and Loom clients share a schema version; adapter/framework compatibility and contract suites pass at pinned versions. |

## Rollback and data safety rules

1. Before each phase, export counts, IDs, source digests, and active leases.
2. Backfills never overwrite source rows and never hard-delete target history.
3. Every dual-write has a semantic reconciliation job and divergence SLO.
4. Cutover flags are workspace-scoped before becoming global.
5. Rollback switches readers/writers; it does not reverse immutable imports.
6. Archived/deleted legacy rows map to archived target records and remain
   queryable for audit.
7. Active leases and in-flight Executions/attempts finish on their admitted
   schema, adapter, framework, and artifact versions or are explicitly drained;
   they are never silently reinterpreted mid-execution.
8. Secrets are not exported in migration reports. Secret references and access
   audit migrate; values remain in the credential boundary.
9. Human approvals cannot be fabricated during backfill. Historical effects
   without approval are marked `legacy_unverified`, not retroactively approved.
10. Removal migrations run only after restore and downgrade drills succeed.

## Recommended implementation order

The smallest sequence that unlocks value without cementing the current mixed
identity model is:

1. HarnessAdapter + HarnessArtifactRevision/ComponentRef projection over
   existing Driver/DriverVersion;
2. Agent/AgentRevision + RoleRevision + PolicySet/Revision + implementation ref;
3. ExecutionIntent + Execution + ExecutionAttempt;
4. AgentSession + RuntimeSessionBinding + RuntimeCheckpoint + TranscriptEvent;
5. Flue adapter at the exact pin, including its persistence/event contract
   tests and canonical mapping;
6. Codex/Claude CLI adapters, then SDK and Vercel eve adapters;
7. AgentSubscription plus RuntimeDeployment/AgentDeployment split;
8. Workflow/WorkflowRevision and explicit multi-Agent Delegation;
9. approvals and execution-scoped effect broker;
10. SDLC reference/projection/relationship graph and legacy retirement.

This order retains the useful current Driver/Trigger/TaskRun machinery while
correcting identity, external-harness, canonical-data, and policy boundaries
before additional agent UX and automation depend on them.
