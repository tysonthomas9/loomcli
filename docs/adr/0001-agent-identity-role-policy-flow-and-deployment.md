# ADR-0001: Separate Loom Agent Identity from Harness Runtime Semantics

**Status:** Accepted

**Date:** 2026-07-09

**Amended:** 2026-07-09 after review of Loom's pinned Flue framework

**Owners:** Loom architecture

**Supersedes:** End-state identity portions of the AgentService and unified-agent proposals

**Related:** [target architecture](../design/2026-07-09-ai-sdlc-agent-control-plane-architecture.md), [glossary](../loom-glossary.md), [migration](../design/2026-07-09-agent-data-model-inventory-and-migration.md)

## Context

Loom is an AI SDLC control plane spanning ideation, planning, implementation,
review, deployment, and post-deployment operations. It must govern interactive
lead agents, terminal coding agents, framework-backed continuing agents,
event-driven GitHub agents, monitoring/delegating Slack agents, scheduled work,
finite workflows, and background tasks.

Loom currently has two competing agent-like records (`Agent` and
`AgentService`), mixed identity/runtime fields, TriggerBindings that also carry
behavior and permissions, and overlapping DriverRun, TaskRun, AgentSession,
filesystem session, and TerminalSession concepts.

The first architecture draft also treated Flue as if it were Loom's internal
workflow substrate and introduced a universal `AgentRun`. That is incorrect.
Flue is an external Agent Harness Framework. At Loom's pinned Flue commit:

```text
AgentProfile -> AgentDefinition -> AgentModule -> AgentInstance
  -> Harness -> Session -> Operation -> Turn

Workflow -> WorkflowRun
```

Flue runs are workflow-only. Direct prompts and asynchronously dispatched agent
inputs are submissions/operations inside continuing sessions, not workflow
runs. Codex and Claude CLIs/SDKs expose still other native session and execution
shapes. Vercel eve and a future Loom-native harness will add more.

Loom therefore needs a stable control-plane model that does not copy one
harness's vocabulary, while FleetDB remains authoritative for all public Loom
identity, session, execution, transcript, policy, and lineage formats.

## Decision

### 1. Loom and FleetDB own the canonical control-plane formats

FleetDB is authoritative for:

- Organization, Role, Agent, and immutable revisions;
- PolicySet, effective policy, grants, approvals, and budgets;
- ExecutionIntent, Execution, ExecutionAttempt, and TaskRun lineage/state;
- AgentSession and mappings to external threads/runtime sessions;
- TranscriptEvent, Artifact, evaluation, usage, and audit;
- normalized SDLC references, projections, and relationships.

Harness-native IDs, events, sessions, workflow runs, resume tokens, and
checkpoints are inputs to the canonical model. They are not primary product
keys and do not define Loom lifecycle semantics.

Some harnesses need private state for correct resume/recovery. FleetDB may store
that state through adapter-specific tables or opaque RuntimeCheckpoint records.
The HarnessAdapter interprets the payload and version. No harness-private schema
becomes the public Loom API or the sole history authority.

### 2. Loom is harness-neutral

A stable `HarnessAdapter` plus immutable `HarnessAdapterRevision` contract
integrates an execution system. Each revision records supported native
versions, capabilities, mapping schema/code digest, and compatibility evidence.
The adapter must support the applicable subset of:

- artifact registration and normalized manifest projection;
- capability discovery and compatibility validation;
- session creation/resume and native identity mapping;
- execution start, observation, cancellation, recovery, and result mapping;
- runtime-attempt ownership, heartbeat, checkpoint, and native correlation;
- native event -> TranscriptEvent normalization;
- usage, artifact, error, and health reporting;
- enforcement/correlation of run-scoped policy grants.

Initial adapters are:

- Flue framework artifacts and runtimes;
- Codex CLI;
- Claude Code CLI.

Expected later adapters include Codex/OpenAI SDKs, Claude Agent SDK, Vercel eve,
and a Loom-native harness. Adapter names never appear in canonical Execution
kind or status enums.

### 3. Loom Agent is a governed organizational identity

An Agent is a durable, addressable Loom actor with responsibility and an
independent governance boundary. It is not a harness definition or runtime
instance.

`AgentRevision` is immutable and resolves:

- RoleRevision, responsibility, and persona;
- PolicyRevision references and restrictions;
- one `AgentImplementationRef`;
- normalized ComponentRefs/digests used for analytics and reproducibility.

For Flue, AgentImplementationRef points to a HarnessArtifactRevision and an
agent module/definition key. A Flue AgentInstance is a runtime context mapped to
a Loom AgentSession/RuntimeSessionBinding; it does not create another Loom
Agent. For a CLI or SDK, the implementation ref names the adapter profile and
versioned configuration artifact.

### 4. Harness authoring remains harness-owned

`HarnessArtifactRevision` is an immutable registered build containing framework
version, compatible HarnessAdapterRevision, source/bundle digests, and a
normalized manifest. Loom
does not recreate every Flue/eve/CLI prompt, tool, skill, session, workflow, or
sandbox field as an independently authored Loom field.

Adapters project normalized ComponentRefs (model, prompt/instructions, skill,
tool, sandbox, harness, adapter) so FleetDB can attribute executions and support
analytics. The executable artifact remains the byte/digest authority for what
the harness actually loaded.

Existing Driver/DriverVersion records are the first storage form for
HarnessArtifact/HarnessArtifactRevision and migrate without discarding their
immutable bundle history.

### 5. Execution replaces universal AgentRun

`ExecutionIntent` is the durable pre-admission request for ready work, external
events, conversations, schedules, manual commands, and delegations.

`Execution` is the harness-neutral admitted unit. It pins AgentRevision or
WorkflowRevision, EffectivePolicySnapshot, source/SDLC refs, session and parent
lineage, semantic kind, requested outcome, status, usage, and result.

Canonical kinds are semantic and limited to:

- `interaction` — input/response in a continuing context;
- `workflow` — finite registered orchestration;
- `task` — bounded task/work-item execution;
- `action` — bounded control-plane or external action when it merits its own
  accountable execution.

`ExecutionAttempt` records one runtime ownership/processing attempt:
HarnessAdapterRevision, artifact/deployment/Node, lease/fencing, native
IDs/types, checkpoint,
timestamps, and outcome. Runtime retries or recovery may create or transfer
attempt ownership without creating a second business Execution.

Mapping examples:

| Native concept | Loom canonical concept |
|---|---|
| Flue direct prompt or dispatch submission | interaction Execution; submission/dispatch/operation IDs are native refs |
| Flue WorkflowRun | workflow Execution; Flue runId is a native ref |
| Codex/Claude CLI prompt in a continuing session | interaction Execution; CLI session/process IDs are native refs |
| Codex/Claude CLI task process | task Execution plus TaskRun when it owns a Loom task |
| SDK agent loop | semantic Execution kind plus SDK thread/trace refs |

Tool/model turns and harness-internal operations normally become TranscriptEvent
spans inside an Execution, not nested Executions. A harness subagent becomes a
new Loom Execution/Delegation only when it crosses a Loom responsibility or
policy boundary; otherwise it remains attributed native activity.

### 6. Sessions are FleetDB-canonical continuity containers

`AgentSession` is the canonical context for one Loom Agent and can contain zero
or more Executions. A session can represent a terminal conversation, Slack or
GitHub thread, ticket, incident context, or other continuing interaction.

`RuntimeSessionBinding` maps it to adapter-native identifiers. For Flue this may
include agent definition name, AgentInstance ID, harness name, session name,
storage key, and native cursor. For a CLI it may include the provider session or
resume ID. FleetDB assigns the canonical session ID and owns its lifecycle.

Flue SessionData, submission journals, CLI resume state, or SDK thread state may
be retained as RuntimeCheckpoints. They are recovery inputs, not a second
canonical AgentSession or transcript.

### 7. TranscriptEvent is the canonical evidence schema

All adapters normalize activity into ordered append-only TranscriptEvents.
FleetDB assigns canonical stream identity and sequence and stores:

- Agent/AgentRevision, Session, Execution/Attempt, parent/delegation, trace;
- normalized event type and structured content;
- policy, grant, approval, tool/effect, usage, artifact, state, and error data;
- ComponentRefs used by the execution;
- native adapter/framework, event type/version, ID/cursor, and redacted payload
  provenance.

FlueEvent, Claude/Codex transcript JSON, SDK events, and future Loom harness
events are source formats. Loom's canonical schema is the analytics, audit, and
product history authority. Raw source payloads may be retained as protected
artifacts or adapter payloads. Hidden chain-of-thought is not stored.

### 8. Workflow is registered finite coordination, not Agent identity

`Workflow`/`WorkflowRevision` register a finite implementation and input/output
contract projection. A revision references a HarnessArtifactRevision definition
or a future Loom-native implementation. Loom does not require every framework
to expose the same private graph representation.

A Flue workflow remains a Flue workflow and produces a workflow Execution in
FleetDB. Multi-agent responsibility changes are explicit Delegations and child
Executions; harness-private subagent calls remain transcript activity unless
promoted to Loom boundaries.

### 9. RuntimeDeployment and AgentDeployment are distinct

`RuntimeDeployment` is the deployment unit for a HarnessArtifactRevision. A
single Flue/eve server artifact may contain several agent and workflow modules.
It owns desired/observed instances, placement, rollout, restart, health, and
runtime endpoint state.

`AgentDeployment` remains optional and represents a dedicated/resident Loom
Agent instance or daemon requirement, such as an interactive lead terminal. It
references or is served by a RuntimeDeployment. Event-only Agents need no
dedicated AgentDeployment, though their shared harness still has a
RuntimeDeployment somewhere.

### 10. Role and PolicySet remain separate

Role describes responsibility and expected outcomes. PolicySet enforces work
eligibility, tools/connectors, repositories, environments, data, models,
budgets, concurrency, secrets, sandboxing, egress, delegation, escalation, and
approvals.

Policy is enforced at admission and point of effect. A harness tool can perform
a protected effect only through a Loom-governed tool/connector broker or an
equivalent adapter enforcement point using an execution-scoped grant. A prompt,
harness tool list, persona, or external framework route cannot widen policy.

### 11. Protected actions require humans

Human ApprovalDecision is mandatory by default for:

1. merge;
2. production deployment;
3. rollback;
4. incident communication;
5. destructive infrastructure change;
6. secret access;
7. budget escalation.

Approval is scoped to the exact action and subject revision, expires, and is
revalidated at the effect boundary. Material subject changes invalidate it.

### 12. External systems remain authoritative for native SDLC objects

Loom stores normalized SDLCObjectRef, SDLCProjection, and SDLCRelationship
records for GitHub, Slack, CI, deployment, incident, and observability objects.
The providers remain authoritative for their objects. FleetDB remains
authoritative for Loom's reference graph, routing state, decisions, and evidence.

### 13. No implicit cross-session Agent memory

An AgentSession may retain and resume its own history. A different session does
not automatically inherit it. Prior evidence enters another session only by an
explicit policy-governed retrieval recorded in TranscriptEvent. There is no
hidden AgentMemory profile or autonomous cross-session recall.

## Consequences

### Positive

- Loom can add Flue, eve, CLI, SDK, and native harnesses without redesigning its
  product data model.
- FleetDB supplies one identity, execution, session, transcript, and policy
  vocabulary across all runtimes.
- External framework upgrades cannot silently redefine Loom history.
- Framework-native recovery remains possible through checkpoints and adapters.
- Agent responsibility survives implementation, deployment, model, prompt,
  skill, and harness changes.
- Common ComponentRefs and TranscriptEvents support comparative analytics.

### Costs

- Harness adapters become substantial, versioned compatibility components.
- FleetDB needs new canonical tables plus adapter-native checkpoint storage.
- Existing DriverRun, AgentSession, filesystem session, and provider transcript
  paths require backfill and reconciliation.
- Runtime policy is trustworthy only when protected effects are brokered or the
  sandbox prevents bypass.
- A single native interaction can have canonical IDs plus several native IDs;
  tooling must display both without conflating them.

### Risks and mitigations

| Risk | Mitigation |
|---|---|
| Lowest-common-denominator canonical schema | Normalize stable accountability/evidence fields; retain versioned native provenance/payloads for lossless debugging. |
| Adapter drift after framework upgrades | Pin adapter/framework compatibility, validate artifact manifests, run contract suites, and block unsupported versions. |
| Duplicate state between FleetDB and harness | Declare FleetDB canonical fields explicitly; adapter checkpoint state is private and reconciled, never a competing read model. |
| Policy bypass through harness-native tools | Route protected effects through Loom brokers and constrain sandbox egress/credentials. |
| Execution becomes too generic | Keep semantic kinds small and use adapter/native refs plus TranscriptEvent types for detail. |
| One deployment is mistaken for one Agent | Model RuntimeDeployment separately from optional AgentDeployment. |

## Alternatives considered

### Use Flue's model as Loom's canonical model

Rejected. Flue is external, experimental, and only one supported harness. Its
workflow-only run semantics do not describe CLI/SDK interactions or a future
Loom-native harness.

### Invent one AgentRun for every runtime activity

Rejected. It contradicts Flue's explicit run semantics and conflates business
work, runtime attempts, operations, turns, and sessions. Execution plus
ExecutionAttempt provides a neutral boundary.

### Store only native harness history

Rejected. Product analytics, audit, retention, policy evidence, and
cross-harness UI would depend on external schemas and upgrades. Native payloads
remain provenance, not the canonical query model.

### Copy every harness configuration field into AgentRevision

Rejected. It creates a second authoring system and guaranteed drift. Loom stores
the artifact/definition reference and normalized component projections/digests.

### Make AgentService the universal Agent identity

Rejected. It mixes identity, implementation, trigger, permissions, and service
lifecycle and cannot represent shared framework deployments cleanly.

### Add implicit cross-session memory

Rejected. Explicit evidence retrieval provides provenance and policy control
without hidden state contamination.

## Compatibility and migration

Breaking API and model changes are acceptable. Migration must still be staged,
idempotent, observable, and reversible by phase. The detailed source mapping,
adapter backfill, cutover gates, and legacy retirement plan are defined in the
[data-model inventory and migration plan](../design/2026-07-09-agent-data-model-inventory-and-migration.md).

Until cutover, legacy records are adapters into the canonical model, not
parallel architectural truths.
