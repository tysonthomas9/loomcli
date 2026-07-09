# ADR-0001: Separate Agent Identity, Role, Policy, Flow, and Deployment

**Status:** Accepted

**Date:** 2026-07-09

**Owners:** Loom architecture

**Supersedes:** End-state identity portions of the AgentService and unified-agent proposals

**Related:** [Target architecture](../design/2026-07-09-ai-sdlc-agent-control-plane-architecture.md), [glossary](../loom-glossary.md), [migration](../design/2026-07-09-agent-data-model-inventory-and-migration.md)

## Context

Loom is an AI SDLC control plane spanning ideation, planning, implementation,
review, deployment, and post-deployment operations. It must govern interactive
lead agents, Flue chat agents, task agents, event-driven GitHub agents,
monitoring/delegating Slack agents, scheduled agents, and background agents.

The current model has two competing agent-like records:

- `Agent` is a persistent role/persona assignment with backend, task filter,
  repository scope, budget, concurrency, parent, desired state, and observed
  runtime fields.
- `AgentService` combines a behavior identity, service kind, Role or
  DriverVersion, desired state, placement, restart, triggers, permissions,
  budget, and runtime state.

`TriggerBinding` additionally carries a DriverVersion, target AgentService,
permissions, and routing. Runs and history are split among `DriverRun`,
`TaskRun`, control-plane `AgentSession`, local filesystem sessions, and
`TerminalSession`.

This makes it difficult to answer whether a webhook is an agent, whether a
scheduled agent must be a service, where organizational authority belongs, and
how all agent transcripts can be analyzed uniformly.

## Decision

### 1. Agent is the neutral durable actor identity

An Agent is a durable, addressable actor with a defined responsibility and an
independent governance boundary. Interactive, event-driven, scheduled,
delegated, and continuously running are modes of activation or deployment, not
different identity types.

An event, webhook, cron expression, workflow step, model, terminal, or process
is not by itself an Agent. A GitHub or Slack actor is an Agent when it has a
durable identity, responsibility, policy boundary, and attributable runs.

### 2. Agent changes create immutable revisions

Agent identity remains stable while its effective definition changes through
immutable `AgentRevision` records. Each revision resolves its RoleRevision,
responsibility, persona, behavior, PolicyRevision references, prompt, harness,
skills, model/provider preferences, tools, and version/digest metadata.

Every AgentRun pins one AgentRevision. Existing runs are never reinterpreted
using the Agent's current configuration.

### 3. Role and PolicySet remain separate

Role defines organizational responsibility, expected outcomes, default work
eligibility, and recommended behavior configuration.

PolicySet is the enforceable boundary for task eligibility, tools/connectors,
repositories, environments, data, budget, authority, secrets, sandboxing,
egress, concurrency, models, delegation, escalation, and approvals.

Organization is the highest tenant/governance scope. Workspaces belong to an
Organization, and approval actors use stable PrincipalRefs plus decision-time
membership/authority evidence rather than mutable display names.

Role may reference default PolicySets, but a prompt, persona, Role description,
or skill cannot grant authority. Policy resolves in layers from organization to
workspace/project to role defaults to agent restrictions to explicit run grants.
Lower layers narrow by default; deny wins.

### 4. Flow, Driver, and Trigger are not Agent identity

- Flow defines how agents and deterministic steps coordinate.
- Driver and DriverVersion package executable behavior.
- TriggerEvent records an occurrence.
- AgentSubscription routes an event, schedule, ready-work class, conversation,
  or manual request to an Agent or Flow.

TriggerBinding evolves into AgentSubscription or Flow routing and stops owning
identity, Role, or authority.

### 5. AgentDeployment is optional runtime lifecycle

`AgentDeployment` represents desired and observed runtime state for agents that
need a persistent or interactive presence. It owns placement, desired state,
instance count, restart/update policy, health, runtime adapter, and leases.

An event-only or schedule-only Agent has subscriptions and creates runs but need
not have a deployment. `AgentService` is not the universal future identity; it
is split into Agent plus optional AgentDeployment and AgentSubscription.

### 6. All ingress converges on AgentRunIntent

Ready-work pull, external events, conversations, schedules, manual commands,
and delegations create a durable `AgentRunIntent`. Admission resolves revisions,
policy, budgets, approvals, idempotency, and concurrency before creating an
AgentRun or FlowRun.

FleetDB's ready frontier remains authoritative for Kanban work. Ready events
only wake a pull scheduler; they do not independently create task runs from
`status=open`. External events may execute without first becoming Kanban items.

### 7. Run, session, and terminal are distinct

- AgentRun is one bounded attempt and policy boundary.
- FlowRun is one bounded execution of a FlowRevision.
- TaskRun is a finite leased/fenced task execution and the only execution record
  authorized to complete a task.
- AgentSession is continuity/context and may contain multiple AgentRuns.
- TerminalSession is a PTY transport attachment.

The existing DriverRun may initially back AgentRun and FlowRun projections while
storage migrates.

### 8. One canonical transcript contract applies to all agents

Lead terminal, Flue chat, background, GitHub, Slack, and task agents emit ordered
append-only `TranscriptEvent` envelopes. Events include identity/revision,
session/run lineage, source and SDLC references, content, tool calls/results,
policy and approval decisions, versioned prompt/harness/skills/tools/model,
usage/cost, artifacts, status, summaries, and errors.

Provider-native formats and OpenTelemetry are adapters/projections. Loom owns
the stable canonical event schema. Hidden chain-of-thought is not stored;
provider-safe summaries, explicit decisions, tools, evidence, and outcomes are.

### 9. External systems remain authoritative

Loom stores normalized `SDLCObjectRef`, `SDLCProjection`, and
`SDLCRelationship` records for GitHub, Slack, CI, deployment, incident, and
observability objects. Provider-native state remains authoritative. Effects use
connectors, explicit grants, freshness checks, and provider preconditions.

### 10. Human approval is mandatory for protected action classes

The default organizational policy requires a durable human ApprovalDecision
for:

1. merge;
2. production deployment;
3. rollback;
4. incident communication;
5. destructive infrastructure change;
6. secret access;
7. budget escalation.

Approval is scoped to the exact action and subject revision, expires, and is
revalidated at the point of effect. Material subject changes invalidate it.

### 11. Loom does not implement cross-session Agent memory

Context is session-local. Durable transcripts, artifacts, decisions, and SDLC
state may be explicitly retrieved as evidence in a later session. There is no
implicit AgentMemory profile, hidden write path, or autonomous recall across
sessions.

## Consequences

### Positive

- One identity model covers terminal, chat, background, event, and scheduled
  agents without forcing a single runtime topology.
- Organizational accountability survives changes to prompts, drivers, models,
  runtimes, triggers, and deployments.
- Policy becomes testable, composable, and auditable independently of persona.
- Flows can coordinate multiple accountable agents without impersonating them.
- Common run/transcript evidence supports analytics across every agent surface.
- External providers retain authority while Loom can route and reason over a
  normalized SDLC graph.
- Protected actions have explicit and queryable human authorization.

### Costs

- New durable models and IDs are required.
- The current `/agents` union projection and AgentService-centric UI need a
  breaking read-path migration.
- AgentService, Agent, WorkerProfile, TriggerBinding, DriverRun, and session data
  require deterministic backfills and a period of dual-write/reconciliation.
- Policy admission and point-of-effect enforcement add implementation and
  operational complexity.
- Runtime adapters must normalize transcript events instead of exposing only
  provider-native logs.

### Risks and mitigations

| Risk | Mitigation |
|---|---|
| Two identity systems persist indefinitely | Set a phase-gated cutover and remove legacy writes after reconciliation reaches zero unexplained divergence. |
| Policy snapshots become large | Store a canonical digest plus the expanded rules required for audit; content-address deduplicated snapshots. |
| Events bypass ready-work rules | Use AgentRunIntent for both paths while preserving FleetDB pull/claim semantics for tasks. |
| Approval becomes a UI-only convention | Enforce at connector/tool effect boundaries and record denied attempts. |
| Provider transcript fields do not map perfectly | Retain normalized structured parts plus an optional protected raw-payload artifact. |
| Agent definition becomes an unbounded blob | Keep first-class revision references and content digests; validate a versioned schema. |

## Alternatives considered

### Make AgentService the universal Agent identity

Rejected. It couples identity to desired runtime state and misrepresents
event-only agents. It also mixes behavior, triggers, permissions, placement,
and supervision into a record whose fields change at different rates.

### Let the workflow plane supersede Role and Agent

Rejected. A graph explains coordination, not organizational responsibility or
authority. Shared execution is desirable; erasing responsibility is not.

### Treat each configuration revision as a new Agent

Rejected. It destroys stable accountability, subscriptions, user references,
and longitudinal analytics. Immutable AgentRevision provides reproducibility
without identity churn.

### Put all policy fields on Role

Rejected. Organization/workspace boundaries, explicit agent restrictions,
temporary grants, approvals, and effect-time decisions have independent
lifecycle and composition rules. Role remains a useful source of defaults.

### Require every external event to create a Kanban task

Rejected. Operational signals and conversations often need immediate routing
without backlog pollution. If planned durable work emerges, the agent can
create or link an issue under policy.

### Use provider transcripts or OpenTelemetry as the canonical schema

Rejected. Provider formats differ and telemetry conventions evolve. They are
valuable import/export formats but cannot be the stable product data contract.

### Add cross-session Agent memory

Rejected for this architecture. It creates privacy, provenance, deletion,
policy, contamination, and reproducibility problems without being necessary
for explicit evidence retrieval.

## Compatibility and migration

Breaking API and model changes are acceptable, but data migration must be
staged, idempotent, observable, and reversible by phase. The detailed mapping,
backfill rules, cutover gates, and legacy retirement list are defined in the
[data-model inventory and migration plan](../design/2026-07-09-agent-data-model-inventory-and-migration.md).

Until cutover, legacy resources are adapters into the target model. They are not
parallel architectural truths.
