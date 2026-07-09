# Agent Data-Model Inventory and Migration

**Status:** Proposed implementation plan

**Inventory snapshot:** 2026-07-09

**Loom source:** `loomcli` branch `unified-agents` compared with `origin/v5`

**FleetDB source:** sibling `fleet-db` branch `unified-agents` compared with `origin/main`

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
| `AgentSession` | One agent execution instance, including task, terminal, phase, attempt, status, heartbeat, outcome, and error. | Currently run-shaped. Redefine as continuity and move execution-attempt state to AgentRun/TaskRun. |
| `TerminalSession` | PTY transport/status/stream/transcript attachment. | Retain strictly as transport; attach to canonical session/run. |
| `Artifact` | Typed artifact reference/content metadata owned by agent/session/task/run-like resources. | Retain; add AgentRun, FlowRun, transcript-event, approval, and SDLC links. |
| `AgentLease` | Session execution lease with fencing. | Migrate to explicit RunLease or generic Lease resource typing. |
| `Lease` | Generic resource ownership lease. | Retain and prefer; add target resource kinds for deployments/runs/tasks. |
| `AgentOwnershipLease` | Ownership of an Agent by a Node/controller. | Convert to AgentDeployment instance lease; identity itself is not ownable runtime state. |
| `AgentCommand` | Desired command for an Agent/runtime. | Split target into AgentDeploymentCommand or AgentRunCommand. |
| `AgentInboxMessage` | Addressed asynchronous message with delivery state. | Migrate to AgentMessage/Delegation while preserving delivery/audit fields. |

### Driver, trigger, service, and run platform

| Model | Current responsibility | Main architectural issue |
|---|---|---|
| `Driver` | Named executable behavior package, owner, active version, status, trust, metadata. | Retain as implementation artifact, not Agent identity. |
| `DriverVersion` | Immutable source/bundle version, digests, runtime, manifest, build/validation state. | Retain; reference from AgentRevision or FlowRevision. |
| `WorkerProfile` | Role/backend/runtime policy/repo/priority/concurrency/capability defaults for task workers. | Rename/refocus as ExecutionProfile; remove identity and organizational authority. |
| `AgentService` | Agent-like behavior plus kind, Role or DriverVersion, desired state, profile, schedule/events/triggers, placement, instance count, lease, restart, permissions, budget, state, and soft delete. | Split into Agent, AgentRevision, AgentDeployment, AgentSubscription, and policy. |
| `TriggerBinding` | Source route/filter/schedule, DriverVersion target, optional AgentService target, concurrency/retry/idempotency/auth, webhook secret, permissions, enabled state. | Evolve to subscription/routing. Remove identity, behavior ownership, secret, and authority. |
| `TriggerEvent` | Normalized immutable occurrence with source/event/subject/payload, provenance, hop depth, and timestamps. | Retain; align envelope with CloudEvents-style source/id/type/subject fields. |
| `TriggerDelivery` | One event-to-binding fan-out attempt, concurrency subject, status/rejection, run, retry, error. | Retain; point to AgentSubscription/FlowTrigger and AgentRunIntent. |
| `ActionLedger` | Idempotent external/deterministic action state and outcome. | Retain; link policy/approval/effect evidence. |
| `DriverRun` | Bounded workflow execution with source, binding/service attribution, subject concurrency, parent/await, node/lease/fencing, payload/output, status, summary, and error. | Back AgentRun/FlowRun during migration; product identity should not be Driver-first. |
| `DriverStep` | One workflow step with kind, TaskRun/ActionLedger/external refs and state. | Retain as FlowRun implementation detail. |
| `TaskRun` | Finite task execution, runner/version/profile, node/lease/fencing, placements, input, usage/cost, logs/artifacts, status, and error. | Retain as canonical task effect unit; link to parent AgentRun/FlowRun step. |
| `TaskRunCompletion` | Immutable fenced terminal completion envelope, task close intent, usage, artifacts, and errors. | Retain; this remains the atomic task completion authority. |
| `TaskRunLogEntry` | Ordered fenced stdout/stderr-style task log entry. | Retain; optionally project into TranscriptEvent without replacing raw logs. |
| `AwaitInstance` | Durable wait condition and its matched/timeout/resume state. | Retain as orchestration internal linked to FlowRun. |
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
| `ConnectorCallRecord` | Append-only record for granted, denied, stale, precondition-required, or failed egress calls. | Retain and link to AgentRun, policy snapshot, approval, and SDLC refs. |

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
| `SessionRecord` | Index entry in `sessions/index.jsonl` with agent/backend/task/status/timing/transcript/diff/usage metadata. | Import to AgentSession + AgentRun + Artifact indexes; retain a compatibility reader temporarily. |
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
Some Loom history paths still enumerate current bindings to find runs. Replacing
or deleting a binding can therefore hide attributable history even though the
run retained an AgentService ID. Target history queries run identity directly
and treat routes as historical context only.

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
| `EffectivePolicySnapshot` | digest, input revision refs, expanded critical rules | Reproducible policy pinned to a run; may be content-addressed/deduplicated. |
| `Agent` | `agent_id`, workspace, display/name/status, created/archived provenance | Stable responsible actor identity. |
| `AgentRevision` | `agent_revision_id`, `agent_id`, ordinal/digest, RoleRevision, responsibility, persona, behavior, policy, versioned config refs | Immutable resolved definition used by runs. |
| `AgentSubscription` | `subscription_id`, target Agent/Flow, source/route/filter/schedule, concurrency/retry/idempotency, enabled | Activation/routing without identity or authority. |
| `AgentDeployment` | `deployment_id`, Agent/revision channel, desired state, placement/restart/update, observed instances | Optional persistent/interactive runtime lifecycle. |
| `AgentRunIntent` | `intent_id`, target, source/SDLC refs, requested capabilities, dedup/concurrency, initiator/delegator, admission state | Durable pre-execution admission object. |
| `AgentRun` | `agent_run_id`, intent, AgentRevision, policy snapshot, session/parent/delegation, implementation run, state/outcome/usage | Product-level bounded accountable attempt. |
| `Flow` | `flow_id`, owner/name/status | Stable coordination identity. |
| `FlowRevision` | `flow_revision_id`, graph/entrypoint, participant selectors, DriverVersion refs, digest | Immutable coordination definition. |
| `FlowRun` | `flow_run_id`, intent, FlowRevision, policy context, parent, state/outcome | Product-level flow attempt. |
| `AgentSession` | `session_id`, agent, channel/context ref, lifecycle, participant/source refs | Continuity container holding zero or more runs. |
| `TranscriptEvent` | `transcript_event_id`, ordered stream/sequence, run/session/identity lineage, type, structured data, redaction | Universal append-only evidence envelope. |
| `Delegation` | `delegation_id`, from/to agents/runs, requested outcome, constraints, budget/deadline, status/result | Explicit agent-to-agent work transfer. |
| `AgentMessage` | `message_id`, sender/recipient refs, session/run/delegation, content ref, delivery state | Addressed communication where delivery must be durable. |
| `ApprovalRequest` | `approval_request_id`, run, action class, exact subject/version, required policy, state/expiry | Durable protected-action gate. |
| `ApprovalDecision` | decision ID, request, approver, decision, reason/conditions/time | Immutable human decision. |
| `SDLCObjectRef` | provider/scope/kind/external ID unique key plus Loom ID | Stable normalized reference to internal or external SDLC object. |
| `SDLCProjection` | object ref, normalized fields, source version, observed/fresh-until | Cached routing/policy/UI view, not provider authority. |
| `SDLCRelationship` | from/to refs, type, provenance, validity | Cross-system SDLC graph edge. |
| `AgentEvaluation` | evaluation ID, run/revision refs, evaluator/method/version, metrics/findings | Immutable feedback for analytics and controlled improvement. |
| `ExecutionProfile` | profile ID, runner/provider/sandbox/placement/resource defaults | Reusable runtime template, replacing WorkerProfile's identity-like semantics. |

There is intentionally no `AgentMemory` model.

### Retained implementation and infrastructure records

`Driver`, `DriverVersion`, `DriverStep`, `TaskRun`, `TaskRunCompletion`,
`TaskRunLogEntry`, `TaskRunEvent`, `OutboxRecord`, `AwaitInstance`,
`ActionLedger`, `Node`, generic `Lease`, `TerminalSession`, `Artifact`,
`Connector`, `ConnectorCallRecord`, `Issue`, `Dependency`, `Comment`, `Event`,
`Snapshot`, and `IdempotencyRecord` remain. Their foreign keys and owner/resource
enums expand to target IDs.

## Source-to-target migration matrix

| Current source | Target | Transformation |
|---|---|---|
| `Role` | Role + RoleRevision + referenced PolicySet/Revision | Keep stable identity/name. Move description/responsibility and recommended behavior to RoleRevision. Convert tool, read-only, path/repo/task eligibility, priority, concurrency, and budget fields to policy rules while preserving compatibility projections. |
| `Workspace` | Workspace under Organization | Add `organization_id`; existing installations receive one deterministic default Organization before workspace backfill. |
| old `Agent` | Agent + AgentRevision | Create stable Agent ID. Resolve RoleRevision and behavior refs. Convert backend/fallback/skills-like fields to revision config. Convert task/repo/filter/budget/concurrency/auto to policy. |
| old `Agent.Mode`, `DesiredState`, runtime `State` | optional AgentDeployment | Create deployment for service/interactive/supervised records; do not put observed runtime on Agent. Ephemeral/event-only agents get no deployment. |
| old `Agent.Parent` | explicit relationship/delegation defaults | Preserve organizational relationship if meaningful; never infer active run delegation from a static parent alone. |
| `AgentService` | Agent + AgentRevision | One stable Agent per service identity. Role-backed service resolves RoleRevision; Driver-backed service resolves DriverVersion behavior. Preserve metadata, creator, archive time, and legacy ID mapping. |
| AgentService kinds `lead`, `orchestrator`, `always_on`, persistent `support/on_call` | AgentDeployment | Move desired state, placement, max instances, lease/restart/runtime state. Validate ambiguous records individually. |
| AgentService kinds `event`, `cron`, `scheduled` | AgentSubscription, usually no deployment | Move event/schedule activation to subscriptions. Create a deployment only if observed configuration truly maintains a resident process. |
| AgentService permissions/budget | PolicySet/Revision | Translate without widening; unknown strings become deny/manual-review, not allow. |
| `WorkerProfile` | ExecutionProfile + policy references where required | Keep runner/backend/runtime/placement/resource defaults; move work eligibility and authority to policy. |
| `TriggerBinding` | AgentSubscription or Flow trigger | Preserve source, route, filter, schedule, retry, concurrency, idempotency, enabled state. Target Agent/Flow replaces Driver/AgentService identity coupling. Move secrets to Connector and permissions to policy-issued grants. |
| unattached binding shown as Agent | classified Agent + subscription, Flow trigger, or automation | Do not blindly mint an Agent. Classify by responsibility metadata; ambiguous records enter a migration-review queue. |
| `TriggerEvent` | TriggerEvent | Preserve IDs/payload/provenance; add normalized envelope version and SDLC refs. |
| `TriggerDelivery` | delivery to subscription + AgentRunIntent | Preserve attempts/status/rejections/run; backfill intent from delivery and run. |
| `DriverRun` | AgentRun or FlowRun plus implementation link | Classify by stamped AgentService, binding target, driver metadata, and parent/step graph. Preserve original run ID through a legacy mapping or use it as implementation_run_id. |
| `DriverStep` | FlowRun step | Preserve order/kind/status and TaskRun/ActionLedger/external links. |
| `TaskRun` | TaskRun child of AgentRun/FlowRun step | Add parent run IDs and effective identity/policy snapshot. Preserve TaskRun ID, lease/fencing, placement, usage, and outcome. |
| control-plane task-kind `AgentSession` | AgentRun/TaskRun lifecycle + optional AgentSession continuity | Move attempt/phase/status/error to run. Only create/retain AgentSession when there is actual continuity context. |
| other control-plane `AgentSession` | canonical AgentSession + linked runs | Preserve terminal/parent/summary metadata; separate session lifecycle from run outcome. |
| filesystem `SessionRecord`/metadata | AgentSession + AgentRun + Artifact | Import deterministic IDs, status/times/backend/task/diff/usage. Store original file refs and import checksum. |
| filesystem `TranscriptEntry`/parsed events | TranscriptEvent | Convert sequence/content/tool/usage events with schema version, adapter provenance, and redaction. Preserve raw transcript as protected Artifact where allowed. |
| `TerminalSession` | TerminalSession | Link to canonical session and current run; preserve PTY/stream state. TranscriptRef becomes an artifact/import pointer, not history authority. |
| `AgentLease` | RunLease/generic Lease | Preserve fencing and expiry; target AgentRun or Deployment instance explicitly. |
| `AgentOwnershipLease` | AgentDeployment instance lease | Do not lease the Agent identity itself. |
| `AgentCommand` | DeploymentCommand or RunCommand | Classify target/action; preserve idempotency/status/audit. |
| `AgentInboxMessage` | AgentMessage or Delegation | Preserve sender/recipient/body/reference/delivery. Structured work requests become Delegation. |
| `ConnectorGrant` | policy-issued capability grant | Preserve action/resource/revocation; attach to policy/AgentRun/subscription provenance. Existing binding-only grants remain deny-by-default until mapped. |
| `ConnectorCallRecord` | ConnectorCallRecord | Add AgentRun, policy, approval, SDLC subject refs. Never discard denied calls. |
| `Artifact` | Artifact | Backfill AgentRun/FlowRun/SDLC ownership and retain content hashes/URIs. |
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

### Revision construction

For each source aggregate:

1. canonicalize ordered fields and explicit defaults;
2. resolve Role and Policy revision references;
3. record all behavior/config artifact versions or content digests;
4. compute a deterministic definition digest;
5. reuse an existing matching revision or create the next immutable ordinal;
6. record source IDs/digests as migration provenance.

An unresolvable prompt, skill, DriverVersion, Role, or policy reference does not
silently fall back. The record becomes `migration_blocked` or is imported as
disabled with a diagnostic.

### External object identity

Upsert external refs on `(workspace, provider, provider_scope, kind,
external_id)`. Projection updates require a monotonic provider version when
available. Historical events and transcripts reference the stable object ID,
not a copied URL. Provider deletion marks the projection tombstoned but does not
erase Loom evidence subject to retention policy.

### Run and transcript ordering

- Preserve source run/session IDs in LegacyIdentityMap and provenance fields.
- Assign TranscriptEvent sequence by source-native sequence where trustworthy;
  otherwise use deterministic file order and record `ordering_quality`.
- Deduplicate imports by `(source artifact digest, source sequence, adapter
  version)`.
- Do not synthesize tool success when only a call is present.
- Reconcile usage totals against source summaries and retain discrepancies as
  import diagnostics rather than rewriting evidence.

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
  history, ready-event declines, and session/run ID relationships;
- inventory and export current counts/checksums by workspace and record kind;
- stop adding new identity or permission fields to AgentService/TriggerBinding.

Exit gate: every current source can be counted, exported, and reconciled; no
unknown writer exists.

### Phase 1 — add target records without changing reads

Add FleetDB schemas/APIs/indexes for Organization, PrincipalRef and membership,
RoleRevision, PolicySet/Revision, EffectivePolicySnapshot, canonical
Agent/AgentRevision, AgentSubscription, AgentDeployment, AgentRunIntent,
AgentRun, Flow/Revision/Run, canonical AgentSession, TranscriptEvent,
AgentMessage, Delegation, approvals, SDLC refs/projections/relationships,
AgentEvaluation, and ExecutionProfile.

Also expand Artifact, Lease, connector audit, and ownership enums. Use set-based
PostgreSQL migrations and equivalent Redis/archive behavior.

Exit gate: CRUD/validation/storage parity across supported backends; OpenAPI and
Loom client contract tests pass; no product read depends on new records.

### Phase 2 — backfill identity, revisions, and policy

Run deterministic migrations in dependency order:

1. default/declared Organizations, PrincipalRefs, then Workspace/Repo refs;
2. Role identities and initial RoleRevisions;
3. PolicySets/Revisions translated from organization, workspace, Role, Agent, AgentService,
   WorkerProfile, connector, and runtime rules;
4. Agents/AgentRevisions from old Agent and AgentService;
5. AgentSubscriptions and AgentDeployments;
6. LegacyIdentityMap and per-record diagnostics.

Do not automatically convert an unattached binding to Agent unless its metadata
establishes responsibility. Queue ambiguous binding/script records for review.

Exit gate: 100% of active old Agent and AgentService rows map to exactly one
Agent; every active target Agent has an AgentRevision and effective baseline
policy; ambiguous rows are explicitly disabled/reviewed, not omitted.

### Phase 3 — dual-write authoring through the new aggregate

Make Agent + AgentRevision the write-side aggregate root. Compatibility writers
materialize old AgentService/Role/Binding fields only while old readers exist.
Subscriptions, deployments, and policies have their own endpoints and
transactions/sagas. Reconciliation compares semantic projections, not raw JSON.

Prevent new binding-only UI agents. Existing ones remain visible through a
legacy adapter until classified.

Exit gate: create/update/archive tests prove atomic or compensatable behavior;
startup healer reports zero unexplained repairs for a full release window.

### Phase 4 — unify run intent and ingress

Introduce AgentRunIntent for:

- ready-work scheduler pulls;
- TriggerEvent deliveries;
- manual and scheduled requests;
- terminal/Flue/Slack conversation turns;
- agent delegations.

Replace task-ready direct-dispatch semantics with wake -> canonical ready query
-> atomic claim -> intent. Preserve external-event direct routing. Stamp every
admitted run with AgentRevision and EffectivePolicySnapshot.

Exit gate: replay/idempotency/concurrency tests cover both ingress classes;
ready-work order matches the FleetDB frontier; no task runs from an unclaimed
task; rejected intents are auditable.

### Phase 5 — unify runs, sessions, and transcripts

Create AgentRun/FlowRun projections over current DriverRun, then dual-write
direct run records if a separate store is still useful. Query history by Agent
and run identity, never by current bindings.

Import filesystem sessions and transcripts. Update every runtime adapter—Codex,
Claude, Flue, OpenCode, terminal, background, GitHub, Slack, and task runner—to
emit canonical TranscriptEvents. Keep raw provider logs as protected artifacts
where policy permits.

Redefine AgentSession as continuity. Stop creating task-kind sessions merely to
represent TaskRun execution. Link TerminalSession as transport.

Exit gate: the same transcript API renders all supported runtime classes;
session/run/task lineage and usage reconcile; removing a subscription does not
remove historical runs; local session files are no longer the sole authority.

### Phase 6 — move persistent supervision to AgentDeployment

Change daemon/controller reconciliation to AgentDeployment desired/observed
state. Move placement, instance count, restart, drain, health, ownership leases,
and runtime commands out of Agent and AgentService.

Event-only and scheduled agents prove they run without deployments. Interactive
lead agents prove terminal attachment and restart behavior through deployments.

Exit gate: no supervisor reads old Agent desired state or AgentService desired
state; deployment leases fence stale instances; stop/drain/restart E2E paths
retain transcripts and run attribution.

### Phase 7 — cut over product reads and protected effects

Switch UI/API to first-class Agents and Flows. Display activation channels and
optional deployments as attached resources. Keep Drivers and subscriptions in
advanced views.

At connector/tool effect boundaries, require valid policy grants and human
ApprovalDecisions for merge, production deploy, rollback, incident
communication, destructive infrastructure changes, secret access, and budget
escalation. Use provider freshness/precondition checks.

Exit gate: all protected-action negative and stale-subject tests fail closed;
all product history reads use target IDs; old and new semantic projections match
for the agreed compatibility window.

### Phase 8 — retire legacy authorities

Stop compatibility writes, then remove after archive/export verification:

- AgentService as identity;
- runtime/desired-state/policy fields on old Agent;
- WorkerProfile identity/eligibility semantics;
- TriggerBinding identity, embedded permissions, and direct secret ownership;
- binding-derived run-history lookup;
- task-kind AgentSession as an execution attempt;
- filesystem SessionRecord/transcript as active authority;
- legacy Worker claims;
- duplicate hand-maintained API shapes not guarded by generation/contract tests.

Retain read-only import adapters and LegacyIdentityMap for the published support
window. Tombstones, archived agents, historical runs, transcripts, decisions,
and denied connector calls are not deleted by cleanup.

Exit gate: zero old writers for a full release window; restore drill from the
pre-cutover export succeeds; compatibility flags can be removed without data or
behavior change.

## Reconciliation and acceptance checks

The migration is complete only when these checks hold per workspace:

| Check | Required result |
|---|---|
| Active identity cardinality | Every old Agent/AgentService maps once; no unintended Agent duplicates. |
| Revision coverage | Every run after admission cutover pins exactly one AgentRevision or FlowRevision. |
| Policy coverage | Every admitted run has an EffectivePolicySnapshot digest; unknown legacy permissions are denied/reviewed. |
| Deployment separation | Event/schedule-only Agents run with no deployment; supervised Agents reconcile only from AgentDeployment. |
| Ready-work parity | Selected task IDs and ordering match canonical FleetDB ready/claim behavior. |
| History preservation | Archived agents and deleted subscriptions still return their runs/transcripts through Agent ID. |
| Transcript ordering | Sequence is unique/monotonic per stream; import diagnostics explain every gap/duplicate. |
| Usage reconciliation | Imported run/session totals equal sources or carry explicit discrepancy records. |
| Task correctness | Only the current fenced TaskRun completion changes task terminal state. |
| Approval enforcement | All seven protected classes reject missing, expired, stale, mismatched, or unauthorized decisions. |
| Connector audit | Granted and denied effects link run, policy, approval when required, and exact SDLC subject. |
| External authority | Projection lag/staleness is visible; no cached projection is used as destructive-action truth. |
| Cross-session memory | No implicit read/write path exists; later evidence retrieval is explicit and audited. |
| API compatibility | FleetDB OpenAPI and Loom client snapshot/generation are from the same schema version. |

## Rollback and data safety rules

1. Before each phase, export counts, IDs, source digests, and active leases.
2. Backfills never overwrite source rows and never hard-delete target history.
3. Every dual-write has a semantic reconciliation job and divergence SLO.
4. Cutover flags are workspace-scoped before becoming global.
5. Rollback switches readers/writers; it does not reverse immutable imports.
6. Archived/deleted legacy rows map to archived target records and remain
   queryable for audit.
7. Active leases and in-flight runs finish on their admitted schema/version or
   are explicitly drained; they are never silently reinterpreted mid-run.
8. Secrets are not exported in migration reports. Secret references and access
   audit migrate; values remain in the credential boundary.
9. Human approvals cannot be fabricated during backfill. Historical effects
   without approval are marked `legacy_unverified`, not retroactively approved.
10. Removal migrations run only after restore and downgrade drills succeed.

## Recommended implementation order

The smallest sequence that unlocks value without cementing the current mixed
identity model is:

1. Agent/AgentRevision + RoleRevision + PolicySet/Revision;
2. AgentSubscription and AgentDeployment split;
3. AgentRunIntent and direct Agent-attributed history;
4. canonical AgentRun/AgentSession/TranscriptEvent;
5. approvals and effect-time connector grants;
6. SDLC reference/projection/relationship graph;
7. Flow/FlowRevision product surface and multi-agent Delegation;
8. legacy retirement.

This order retains the useful current Driver/Trigger/TaskRun machinery while
correcting the identity and policy boundary before additional agent UX and
automation depend on it.
