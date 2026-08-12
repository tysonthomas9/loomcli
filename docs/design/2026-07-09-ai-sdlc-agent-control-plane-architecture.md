# Harness-Neutral AI SDLC Agent Control Plane Architecture

**Status:** Proposed target architecture

**Date:** 2026-07-09

**Decision:** [ADR-0001](../adr/0001-agent-identity-role-policy-flow-and-deployment.md)

**Data migration:** [Agent data-model inventory and migration](2026-07-09-agent-data-model-inventory-and-migration.md)

**Vocabulary:** [Loom glossary](../loom-glossary.md)

## Executive summary

Loom is an AI SDLC control plane, not an agent harness. It governs work across
ideation, planning, implementation, review, deployment, and post-deployment
operations while supporting multiple execution systems.

FleetDB owns the canonical public formats for Agent identity and revisions,
Role and policy, execution intent, Execution and attempts, AgentSession,
TranscriptEvent, approvals, artifacts, evaluations, and normalized SDLC state.
External harnesses never become the product data authority.

The target architecture is harness-neutral:

- Flue is the first full framework adapter;
- Codex and Claude Code CLIs use direct adapters;
- Codex/OpenAI and Claude SDKs can use SDK adapters;
- Vercel eve is an expected next framework adapter;
- a future Loom-native harness implements the same contract.

An immutable `AgentRevision` references a registered implementation rather than
copying a framework's authoring schema. `ExecutionIntent` admits work.
`Execution` is the generic, accountable business unit. `ExecutionAttempt`
records runtime ownership, retries, resume, leases, native correlation, and
checkpoints. `AgentSession` is FleetDB-canonical continuity. Harness adapters
translate native events into FleetDB-canonical TranscriptEvents.

This corrects two errors in the earlier direction:

1. a workflow/runtime plane can replace duplicated execution plumbing, but it
   cannot replace organizational Agent, Role, or policy;
2. Flue concepts such as AgentDefinition, AgentInstance, Session, Operation,
   and WorkflowRun cannot be relabeled as universal Loom `AgentRun` semantics.

## Original purpose of Loom

Loom manages governed AI work across the complete SDLC:

1. capture ideas, signals, requirements, and constraints;
2. plan and decompose work;
3. implement and validate changes;
4. review, approve, merge, and release;
5. deploy and observe production;
6. respond to regressions and incidents;
7. retain evidence so people and systems can evaluate and improve agents,
   harnesses, prompts, skills, tools, models, and policies.

The control plane must answer, for any work:

- who was responsible;
- why it started and which SDLC objects it concerned;
- which immutable Agent, policy, implementation, and component revisions ran;
- which harness, deployment, session, and runtime attempts were involved;
- what tools/effects occurred, what it cost, and what evidence it produced;
- which human approved protected effects;
- whether a retry, resume, or framework upgrade changed the outcome.

These questions must have the same answers whether execution came from Flue,
Vercel eve, Codex CLI, Claude Code, an SDK, or Loom's future harness.

## Assessment of the current direction

### Foundations worth keeping

The `unified-agents` branches add necessary control-plane capabilities:

- FleetDB-backed shared records instead of local-only runtime truth;
- immutable DriverVersion artifacts;
- normalized trigger events, deliveries, idempotency, retry, and concurrency;
- driver/task execution with leases, fencing, placement, usage, and artifacts;
- connector grants and append-only call audit;
- Node-owned local effects with shared desired/observed state;
- a unified product view across interactive, background, event, and scheduled
  agents;
- early run attribution to agent-like records.

### Identity/runtime concerns that must be separated

The current model mixes independent concerns:

1. `Agent` contains organizational identity plus backend, eligibility, budget,
   desired state, and observed runtime fields.
2. `AgentService` combines agent-like identity, Role/Driver behavior, triggers,
   permissions, deployment intent, restart, placement, and runtime state.
3. `TriggerBinding` routes events but also carries behavior and authorization.
4. `DriverRun`, `TaskRun`, control-plane `AgentSession`, filesystem sessions,
   and `TerminalSession` overlap execution, continuity, transport, and history.
5. `/agents` unions old Agent, AgentService, and unattached bindings into a
   useful UI projection but not a durable identity model.

The execution plane should converge, but Agent, Role, policy, implementation,
subscription, deployment, session, and execution remain separate resources.

### Flue boundary correction

Flue is an external open-source Agent Harness Framework. Loom pins Flue commit
`492bf47b9f3d6c379d00471523987b8fe9511f7d`. At that revision Flue owns:

- AgentProfile, AgentDefinition, agent modules, and AgentInstance;
- Harness, Session, Operation, Turn, skills, tools, subagents, and sandbox;
- direct/dispatched submission durability and session persistence;
- Workflow and WorkflowRun;
- FlueEvent and durable event streams;
- framework build output and Node/Cloudflare runtime behavior;
- channel packages for verified provider ingress.

Flue explicitly says runs are workflow-only. Direct prompts and `dispatch(...)`
inputs operate inside persistent sessions and are not WorkflowRuns. The prior
universal AgentRun proposal therefore encoded a false abstraction.

Loom's existing [native Flue integration](native-flue-driver-integration.md)
already states the correct package boundary: Flue owns authoring, dependency
resolution, build output, and runtime semantics; Loom/FleetDB own registration,
control-plane state, policy, orchestration, task ownership, and evidence.

### Directional verdict

Continue the Driver/Trigger/TaskRun/Connector/Node work, but make it the first
implementation of a harness-neutral adapter architecture. Before retiring the
old Agent plane, add the canonical Agent/AgentRevision, PolicySet,
HarnessArtifactRevision, ExecutionIntent, Execution, ExecutionAttempt,
AgentSession, RuntimeSessionBinding, and TranscriptEvent contracts.

## Architectural principles

1. **FleetDB is canonical.** Harness-native state is mapped data or private
   checkpoint state, never the product authority.
2. **Responsibility defines a Loom Agent.** Runtime duration, framework type,
   ingress, and process topology do not.
3. **Harnesses are adapters.** Flue, eve, CLIs, SDKs, and Loom-native execution
   plug into one versioned contract.
4. **Semantic execution kinds are harness-neutral.** `interaction`, `workflow`,
   `task`, and `action` describe why a unit exists, not which implementation ran.
5. **Definitions are immutable at execution time.** Every Execution pins Agent
   or Workflow revision, policy snapshot, artifact revision, and components.
6. **Role describes; policy enforces.** Prompts, personas, routes, and harness
   tool lists never grant Loom authority.
7. **External SDLC systems remain authoritative.** Loom owns normalized refs,
   projections, relationships, decisions, and evidence.
8. **All ingress converges before admission.** Ready work and external events
   differ in discovery but share intent, policy, execution, and audit.
9. **Every adapter produces one evidence vocabulary.** Native events remain
   provenance; TranscriptEvent is the FleetDB query/analytics contract.
10. **Private recovery state stays private.** Store it through namespaced,
    versioned RuntimeCheckpoints; do not expose it as canonical session history.
11. **Protected effects stop for humans.** Approval is enforced at a broker or
    equivalent effect boundary, not implied by a prompt.
12. **No hidden cross-session memory.** Cross-session evidence retrieval is
    explicit, policy-governed, and recorded.

## Logical architecture

Loom has four operating planes with a cross-cutting evidence contract:

```text
                    Loom / FleetDB canonical control plane

 SDLC state       Organization/policy       Orchestration
 refs/projections Agent/Role/Policy         intent/subscription/workflow
        \                 |                         /
         +----------------+------------------------+
                          |
                       Execution
                          |
                HarnessAdapter contract
                          |
       +------------------+------------------+----------------+
       |                  |                  |                |
     Flue            Codex/Claude         SDK agents      Vercel eve /
  framework             CLIs                                Loom native
       |                  |                  |                |
 native sessions, attempts, events, checkpoints, artifacts, outcomes
       \__________________|__________________|_______________/
                          |
             FleetDB TranscriptEvent / Artifact / Evaluation
```

## 1. SDLC state plane

The state plane represents the delivery graph across systems:

- ideas, requirements, specifications, plans, epics, issues, and tasks;
- repositories, branches, changes, pull requests, reviews, checks, and builds;
- releases, deployments, environments, incidents, alerts, and conversations;
- relationships such as decomposes-to, implements, reviews, blocks, deploys,
  caused-by, fixes, duplicates, and supersedes.

FleetDB issues remain canonical for Loom-managed ready work. Provider-native
objects remain canonical in their providers. `SDLCObjectRef` gives Loom a
stable ID; `SDLCProjection` stores normalized fields needed for routing,
policy, display, and analytics; `SDLCRelationship` joins the graph.

An external reference is uniquely identified by:

```text
(workspace, provider, provider_scope, object_kind, external_id)
```

Every projection records source version/ETag where available, observed time,
and freshness. Destructive writes re-read provider state and use provider
preconditions; a cached projection is never sufficient authority.

## 2. Organization and policy plane

```text
Organization -> Workspace -> PrincipalRef / membership
      |
      +--------------------> organization PolicySet

Role --versioned-by--> RoleRevision
  |                       |
  +-- assigned-to ------> Agent --versioned-by--> AgentRevision
                                               |
PolicySet --versioned-by--> PolicyRevision -----+
                                               |
                                  AgentImplementationRef
                                               |
                                  HarnessArtifactRevision
```

### Role

Role names a responsibility, expected outcomes, escalation expectations, and
default work eligibility. It may recommend implementation profiles, but it is
understandable independently of Flue, a CLI, an SDK, or a model.

### Agent and AgentRevision

Agent is a durable workspace-scoped Loom identity. Names can change; IDs do not.
Editing configuration creates an immutable AgentRevision. A revision contains:

- RoleRevision and explicit responsibility;
- persona reference;
- PolicyRevision references and narrower restrictions;
- AgentImplementationRef;
- normalized eligible SDLC scopes;
- normalized ComponentRefs/digests for model, prompt/instructions, skills,
  tools, harness, adapter, and sandbox where the adapter can report them;
- provenance, creator, timestamp, and supersession metadata.

AgentRevision does not copy Flue AgentRuntimeConfig, eve directory layout, CLI
config, or SDK types. The immutable artifact and implementation reference are
the execution truth. ComponentRefs are Loom-owned normalized projections for
reproducibility and analytics.

### PolicySet and effective policy

Policy resolves in layers:

```text
organization
  -> workspace/project
    -> role defaults
      -> agent restrictions
        -> execution-scoped capability grant
```

Lower layers narrow by default. Widening requires an explicitly authorized,
scoped, expiring grant. Deny wins. Admission stores an
EffectivePolicySnapshot digest and required expanded rules on Execution.

Policy governs work eligibility, repositories/environments/data, tools and
connector actions, models, spend/tokens/time/concurrency/retry, sandbox and
egress, secret classes, delegation/escalation, and approvals.

Organization is the highest tenant/governance scope. Workspaces belong to one
Organization. Approvers use stable PrincipalRefs and decision-time membership
and authority evidence.

## 3. Implementation registry and harness adapters

### Registered artifacts

`HarnessArtifact` is the stable registration identity. An immutable
`HarnessArtifactRevision` records:

- harness kind and version;
- compatible HarnessAdapterRevision and native-version range;
- source reference/digest and build/bundle reference/digest;
- trust, validation, signature/attestation, and provenance;
- normalized manifest entries for agents, workflows, capabilities, transports,
  checkpoint schema, and required runtime features;
- ComponentRefs extracted by the adapter;
- native manifest as a versioned protected payload when needed.

Existing Driver and DriverVersion are retained as the first storage and API
form, then renamed/projected without losing immutable history.

### HarnessAdapter contract

`HarnessAdapter` is the stable integration identity. Each immutable
`HarnessAdapterRevision` pins its implementation/code digest, supported native
versions, capabilities, mapping schema, and contract-test evidence. An adapter
revision declares capabilities rather than pretending every harness behaves the
same:

| Capability | Contract |
|---|---|
| Artifact | inspect, validate, register, and project manifest/components |
| Session | create/bind/resume/delete native context under canonical AgentSession |
| Execute | start an ExecutionAttempt idempotently from canonical Execution |
| Observe | report status/heartbeat, usage, artifacts, errors, and native refs |
| Control | cancel, pause/resume, drain, or report unsupported operations |
| Evidence | translate native events to TranscriptEvent with loss/provenance markers |
| Checkpoint | store/load opaque versioned private recovery state |
| Policy | consume execution-scoped token/grants and route protected effects through Loom |
| Health | report adapter/runtime/artifact compatibility and deployment health |

Unsupported capabilities fail explicitly. For example, a one-shot CLI adapter
may not support native pause/resume, and a hosted SDK may expose no filesystem
checkpoint. The canonical model records that truth.

### Initial and expected adapters

| Adapter | Native concepts mapped |
|---|---|
| Flue | AgentDefinition/module, AgentInstance, Harness, Session, submission/dispatch, Operation, WorkflowRun, FlueEvent, persistence/checkpoints |
| Codex CLI | CLI config/version, session/thread, prompt/task invocation, process attempt, stream JSON, patch/artifacts |
| Claude Code CLI | CLI config/version, session/resume ID, prompt/task invocation, process attempt, stream JSON, artifacts |
| Codex/OpenAI SDK | SDK agent/thread/response/trace/tool events and provider usage |
| Claude Agent SDK | SDK session/query/messages/tool events and provider usage |
| Vercel eve | agent directory/build/deployment, durable session/workflow, channels, tools, approvals, events |
| Loom native | Loom-owned harness implementation that writes canonical formats directly |

Adapter support does not mean external frameworks own control-plane data. It
means Loom can execute them without throwing away their runtime semantics.

## 4. Orchestration plane

### Unified ingress

```text
FleetDB ready frontier -> pull scheduler -------\
provider webhook/channel -> normalized event ---+
cron/manual command -----------------------------+--> ExecutionIntent
terminal/chat conversation ----------------------+
Agent delegation --------------------------------/
```

ExecutionIntent contains:

- target Agent or Workflow and requested revision/channel;
- source kind, TriggerEvent/conversation/delegation, and SDLC refs;
- optional ready-work candidate;
- requested semantic execution kind, outcome, and capabilities;
- idempotency and concurrency subject keys;
- initiator, delegator, parent Execution, and trace context;
- priority, budget request, and admission deadline.

Admission resolves immutable revisions and artifacts, evaluates policy and
budget, creates ApprovalRequests when required, applies concurrency rules, and
rejects, queues, or creates Execution.

### Ready work remains pull-based

FleetDB's ready frontier, dependencies, status, priority, filters, claim, and
lease semantics remain authoritative. Task events wake the scheduler. The
scheduler re-queries the canonical frontier and atomically claims eligible work
before admission. A journal snapshot with `status=open` is not another queue.

### External events remain event-driven

GitHub checks/comments, Slack events, alerts, and deployment signals need not
become Kanban issues. One component owns provider ingress for a configured
route:

- a Loom Connector may verify/normalize it and create TriggerEvent; or
- an external harness channel such as Flue/eve may verify it and call a
  Loom-authenticated ingestion/dispatch boundary.

Never terminate the same webhook independently in both. FleetDB owns delivery
deduplication, routing, intent, policy, and canonical evidence after acceptance.
If planned work emerges, the Agent may create/link an Issue under policy.

### Workflow and delegation

Workflow is a registered finite coordination identity. WorkflowRevision pins an
implementation reference and normalized input/output/capability projection.
The harness owns its private code/graph representation.

Responsibility changes between Loom Agents are explicit Delegations and child
Executions. A Flue subagent profile or SDK-internal helper remains native
transcript activity unless it has its own Loom Agent identity/policy boundary.

## 5. Execution plane

### Execution

Execution is one admitted, accountable unit. Core fields include:

```text
execution_id
intent_id
kind                        # interaction | workflow | task | action
agent_id + agent_revision_id
workflow_revision_id?       # for registered finite workflow
session_id?
parent_execution_id?
delegation_id?
effective_policy_snapshot_id
implementation_ref + artifact_revision_id
source + SDLC refs
requested_outcome
status + reason/error
usage/cost summary
result/artifact refs
created/started/finished timestamps
```

Canonical status describes control-plane truth: queued, awaiting_approval,
admitted, running, suspended, completed, failed, cancelled, or expired. Adapter
native states are recorded separately and reconciled into these transitions.

### ExecutionAttempt

ExecutionAttempt is runtime ownership, not a second business request:

```text
attempt_id + ordinal
execution_id
adapter_revision_id
runtime deployment / node
lease_id + fencing_token
native execution type + native IDs
runtime session binding
checkpoint ref
observed state and heartbeat
start/end/error/outcome
```

FleetDB creates/authorizes attempts. The adapter starts native work idempotently
using Execution/Attempt correlation. Stale owners cannot update status or append
attempt-fenced effects. A recovery may transfer or create attempt ownership
according to adapter capability while preserving one Execution.

### Mapping without semantic distortion

| Scenario | Canonical mapping |
|---|---|
| Flue direct prompt or `dispatch(...)` | interaction Execution; Flue submission, dispatch, operation, instance, harness, and session IDs are native refs/events |
| Flue workflow invocation | workflow Execution; Flue runId is the native execution ref |
| Interactive Codex/Claude terminal turn | interaction Execution in AgentSession; one or more CLI process attempts |
| Background coding task via CLI/SDK | task Execution, linked TaskRun, adapter attempt and artifacts |
| GitHub CI remediation | task or action Execution based on requested outcome; GitHub event is source, not execution kind |
| Slack monitoring/delegation | interaction Execution in provider-thread AgentSession; Delegation creates child Execution when responsibility changes |
| Vercel eve scheduled work | workflow Execution through eve adapter; native durable run/session refs retained |

Native model turns, tool calls, compactions, subagent calls, and logs normally
become TranscriptEvents/spans, not nested Executions. This keeps Execution at a
business/accountability boundary.

### TaskRun

TaskRun remains a specialized leased/fenced record for executing a Loom task
and the only execution record authorized to commit task completion. It links to
its parent Execution and ExecutionAttempt. This preserves current atomic
completion semantics while generic execution matures.

### AgentSession and runtime bindings

AgentSession is FleetDB-canonical continuity for one Agent. It can span many
Executions and process restarts. `RuntimeSessionBinding` maps the session to the
native context used by each adapter:

- Flue agent module, AgentInstance ID, harness name, session name, storage key,
  and native stream cursor;
- Codex/Claude CLI session/resume IDs;
- SDK thread/session IDs;
- eve conversation/session IDs;
- future Loom harness native session ID.

A single Flue AgentInstance can host multiple named Flue sessions. The adapter
therefore stores the exact binding rather than assuming AgentInstance equals
AgentSession. FleetDB owns canonical session ID, participants, source/thread
refs, lifecycle, retention, and transcript.

### RuntimeCheckpoint

Private harness state required for resumption is stored under:

```text
(adapter, adapter_version, schema_version, session/execution/attempt, digest)
```

For Flue this can include SessionData, submission/turn journal state, durable
stream cursors, or a storage adapter namespace. For a CLI it may be a resume
token and protected provider transcript. Checkpoints are encrypted and
retention-controlled. They are not portable memory and are never queried as the
canonical session/transcript API.

### RuntimeDeployment and AgentDeployment

RuntimeDeployment deploys a HarnessArtifactRevision and records desired state,
instances, endpoint/placement, rollout/restart/drain, health, and leases. One
Flue/eve artifact can expose multiple agent/workflow definitions.

AgentDeployment is optional and represents a dedicated/resident Loom Agent
instance requirement, such as a supervised lead terminal. It references a
RuntimeDeployment or a direct local adapter placement. Event-only Agents need
no dedicated AgentDeployment, but their shared harness runtime is still
deployed somewhere.

Nodes own process, sandbox, PTY, filesystem, and worktree effects. Fencing
rejects logs/completions/state changes from stale attempts.

## Canonical evidence and learning contract

### TranscriptEvent

Every adapter translates native output to the same FleetDB-owned ordered,
append-only envelope. Required dimensions include:

- canonical event ID, stream/sequence, event/observed time, schema version;
- Organization/workspace, Agent/AgentRevision, AgentSession,
  Execution/ExecutionAttempt/TaskRun, parent/delegator, trace/span;
- normalized event type and structured content parts;
- TriggerEvent and SDLC refs;
- tool/call/effect, sanitized input/result, duration, and decision;
- policy evaluation, grant, ApprovalRequest/Decision, and protected effect;
- model/provider, usage/cost, latency, retry, and cache data;
- ComponentRefs for artifact, harness, adapter, prompt/instructions, skills,
  tools, model, and sandbox;
- artifacts, state changes, summary, outcome, and normalized error;
- redaction, classification, retention, integrity, and provenance;
- native adapter/framework, event type/version, IDs/cursors, mapping version,
  and optional redacted native payload/artifact ref.

Canonical event families include execution/session/attempt lifecycle,
user/agent message, model turn, tool/effect, delegation, policy, approval,
artifact, usage, state change, summary, and error.

FleetDB assigns canonical sequence and is the history authority. Adapters must
be idempotent under native replay and record mapping loss. For example, Flue's
stable event envelope maps directly where possible; provider-shaped message
payloads remain versioned provenance until normalized.

Loom does not store hidden chain-of-thought. It may store provider-safe
reasoning summaries, explicit decisions, tools/evidence, and outcomes. Raw
payloads are protected artifacts when policy permits.

OpenTelemetry and harness-native events are import/export vocabularies, not the
FleetDB schema.

### Analytics

Executions can be compared by AgentRevision, RoleRevision, PolicyRevision,
HarnessArtifactRevision, adapter/framework version, ComponentRefs, model,
provider, prompt, skills, tools, sandbox, ingress, SDLC type, environment,
budget, approval path, and outcome. AgentEvaluation references immutable IDs
and never mutates historical evidence.

## Policy enforcement and human approval

### Admission and effect enforcement

Admission answers whether work may start. Effect enforcement separately
answers whether a particular tool/connector action may occur now.

Harness integrations receive an Execution-scoped credential/capability token.
Protected tools call the Loom tool/connector broker, which checks:

- Execution and current attempt identity;
- effective policy and resource scope;
- required ApprovalDecision and exact subject version;
- budget and rate/concurrency limits;
- source freshness and provider preconditions;
- idempotency and action ledger.

Harness-native tools capable of bypassing the broker are not considered
governed. Production policy must remove their credentials and constrain
sandbox/egress, or the adapter must provide an equivalent enforceable boundary.

### Mandatory approvals

| Protected action | Approval subject |
|---|---|
| Merge | repository, pull request, expected head/base revisions |
| Production deploy | artifact/release digest, environment, deployment plan |
| Rollback | environment, target revision, impact scope, rollback plan |
| Incident communication | audience/channel and proposed message or bounded template |
| Destructive infrastructure change | exact resource plan/command digest and blast radius |
| Secret access | secret reference/class, purpose, grantee, and duration; never value |
| Budget escalation | prior/requested limit, Execution/Delegation scope, expiry |

Approval expires and is revalidated at the effect boundary. Subject changes
invalidate it. Approvers cannot exceed their own policy. Granted and denied
attempts are recorded.

## No implicit cross-session memory

An AgentSession can continue and resume its own canonical history. That is
session continuity, not cross-session memory. A new AgentSession receives prior
evidence only through an explicit retrieval operation that records query,
source artifacts/events, policy decision, and injected summary/content.

Harness checkpoints cannot be attached to another canonical session as hidden
memory. There is no AgentMemory profile in this architecture.

## Product and API projections

Primary product resources should be:

- `/agents`, `/agents/{id}/revisions`, implementation refs, subscriptions, and
  optional dedicated deployments;
- `/roles`, `/policy-sets`, immutable revisions, and effective snapshots;
- `/harness-adapters`, immutable adapter revisions, `/harness-artifacts`,
  artifact revisions, and `/runtime-deployments`;
- `/workflows` and immutable WorkflowRevisions;
- `/execution-intents`, `/executions`, `/executions/{id}/attempts`, and
  `/task-runs`;
- `/sessions`, runtime bindings/checkpoints through privileged APIs, and
  `/sessions/{id}/transcript-events`;
- `/approvals`, `/delegations`, `/messages`, `/artifacts`, and `/evaluations`;
- `/organizations`, `/principal-refs`, `/sdlc-objects`, and relationships.

The UI presents Agents and Workflows separately. It shows framework/adapter,
runtime deployment, native IDs, and checkpoints as implementation/operator
details. Agent screens can show interactive, background, event, and scheduled
activation modes without creating identity subclasses.

## Ownership boundaries

| Concern | Authority |
|---|---|
| Agent, Role, policy, revisions, subscriptions, intent | FleetDB through Loom APIs |
| Execution, attempts, session, transcript, approvals, lineage, usage summary | FleetDB |
| Harness source authoring and private runtime semantics | Harness/framework project and immutable registered artifact |
| Harness-private resume/checkpoint payload interpretation | Versioned HarnessAdapter |
| Storage/lifecycle/retention of checkpoint envelopes | FleetDB |
| Flue/eve/CLI/SDK native IDs and events | Native runtime as observed input; FleetDB stores mappings/provenance |
| Ready-work graph and Loom task status | FleetDB |
| GitHub/Slack/CI/deploy/incident native objects | External provider; FleetDB owns normalized refs/projections |
| Local process, sandbox, PTY, worktree, filesystem effects | Leased Node/ExecutionAttempt |
| Human/service authentication and group membership | Identity provider; FleetDB retains PrincipalRefs and decision-time evidence |
| Cross-session memory | None; explicit evidence retrieval only |

## Non-negotiable invariants

1. Every Execution pins exactly one AgentRevision or WorkflowRevision and one
   EffectivePolicySnapshot.
2. Every AgentRevision references an immutable implementation/artifact or an
   explicitly versioned native Loom definition.
3. Every ExecutionAttempt pins an immutable HarnessAdapterRevision; Execution
   kinds and statuses never contain adapter/framework names.
4. Native IDs are correlation refs, never FleetDB primary identity.
5. Every runtime attempt is owned/authorized by FleetDB and stale fenced owners
   cannot mutate canonical state.
6. AgentSession and TranscriptEvent are FleetDB-canonical even when private
   harness checkpoints exist.
7. Native replay cannot duplicate canonical TranscriptEvents or external
   effects.
8. Trigger/subscription does not define Agent identity or grant authority.
9. RuntimeDeployment may serve multiple Agents/Workflows; AgentDeployment is
   optional and dedicated.
10. Only a valid fenced TaskRun completion commits a task completion.
11. Protected effects re-check policy, approval, freshness, and preconditions.
12. No prompt, tool list, framework route, or artifact can widen Loom policy.
13. No implicit cross-session memory is read or written.
14. Dual-written migration records reconcile before read-path cutover.

## Explicit non-goals

- Reimplementing Flue, eve, Codex, Claude, or SDK internal schemas in FleetDB.
- Using one external framework's vocabulary as the canonical Loom model.
- Mirroring every provider field or replacing provider object authority.
- Treating every webhook, cron, subagent profile, tool call, or workflow step as
  a Loom Agent or Execution.
- Requiring all harnesses to support pause/resume, durable checkpoints, or the
  same native deployment topology.
- Persisting hidden model reasoning or autonomous cross-session memory.
- Trusting policy that is not enforced at an effect/broker or sandbox boundary.

## Relationship to existing proposals

This proposal preserves the distributed control-plane, immutable artifact,
trigger, task-run, connector, lease, and Node investments in:

- [FleetDB Agent Platform V2](fleetdb-agent-platform-v2-proposal.md);
- [Distributed Control Plane](distributed-control-plane.md);
- [Native Flue Driver Integration](native-flue-driver-integration.md);
- [Unified Agent UX](2026-07-01-unified-agent-ux-proposal.md);
- [Agent Identity Record](2026-07-07-agent-identity-record.md).

It supersedes end-state claims that make AgentService universal identity,
workflow execution a replacement for Role, TriggerBinding an authority owner,
DriverRun a universal agent history model, or Flue an internal Loom subsystem.

## External references

- [Flue README at Loom's pinned commit](https://github.com/withastro/flue/blob/492bf47b9f3d6c379d00471523987b8fe9511f7d/README.md)
- [Flue canonical terminology at the pinned commit](https://github.com/withastro/flue/blob/492bf47b9f3d6c379d00471523987b8fe9511f7d/AGENTS.md)
- [Flue agent model](https://github.com/withastro/flue/blob/492bf47b9f3d6c379d00471523987b8fe9511f7d/apps/docs/src/content/docs/concepts/agents.mdx)
- [Flue workflows and workflow-run semantics](https://github.com/withastro/flue/blob/492bf47b9f3d6c379d00471523987b8fe9511f7d/apps/docs/src/content/docs/guide/workflows.md)
- [Flue events and direct-agent activity](https://github.com/withastro/flue/blob/492bf47b9f3d6c379d00471523987b8fe9511f7d/apps/docs/src/content/docs/api/events-reference.md)
- [Flue channel ownership boundary](https://github.com/withastro/flue/blob/492bf47b9f3d6c379d00471523987b8fe9511f7d/apps/docs/src/content/docs/guide/channels.md)
- [Vercel eve](https://vercel.com/eve)
- [Vercel AI SDK harness adapters](https://vercel.com/blog/ai-sdk-7)
- [CloudEvents specification](https://github.com/cloudevents/spec/blob/main/cloudevents/spec.md)
- [OpenTelemetry generative AI attributes](https://opentelemetry.io/docs/specs/semconv/registry/attributes/gen-ai/)
