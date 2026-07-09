# AI SDLC Agent Control Plane Architecture

**Status:** Proposed target architecture

**Date:** 2026-07-09

**Decision:** [ADR-0001](../adr/0001-agent-identity-role-policy-flow-and-deployment.md)

**Data migration:** [Agent data-model inventory and migration](2026-07-09-agent-data-model-inventory-and-migration.md)

**Vocabulary:** [Loom glossary](../loom-glossary.md)

## Executive summary

Loom should continue toward a shared agent platform, but the abstraction
boundary needs one correction: the execution plane may become unified; the
organizational concepts of Role, Agent, and policy must not be collapsed into
workflow or runtime records.

The target architecture makes **Agent** the neutral, durable identity of a
responsibility-bearing actor. An immutable **AgentRevision** resolves its Role,
persona, behavior, policy, prompts, harness, skills, model, and tool references.
A **Flow** coordinates agents and deterministic steps. A **Driver** packages
executable behavior. A **Trigger** or **AgentSubscription** starts work. An
optional **AgentDeployment** manages persistent runtime lifecycle. These are
separate because they change at different rates and answer different control
plane questions.

Every path—lead terminal, Flue chat, Kanban pull, GitHub event, Slack event,
scheduled maintenance, and delegated background work—converges on the same
admission, run, session, transcript, artifact, policy, and approval contracts.
That common evidence model is what makes later analytics and improvements to
agents, harnesses, skills, prompts, tools, and models possible.

## Original purpose of Loom

Loom is an AI SDLC control plane. Its job is not merely to launch coding CLIs or
execute workflow graphs. It manages governed work across the complete software
delivery lifecycle:

1. capture ideas, signals, requirements, and constraints;
2. plan and decompose work;
3. implement and validate changes;
4. review, approve, merge, and release;
5. deploy and observe production;
6. respond to regressions and incidents;
7. retain evidence so people and systems can evaluate and improve the process.

The control plane must answer, for any action: who was responsible, why work
started, what authority applied, which immutable definition ran, what tools and
external objects were involved, what it cost, what evidence it produced, and
which human approved protected effects.

## Assessment of the current direction

### What is going in the right direction

The `unified-agents` work establishes several necessary foundations:

- FleetDB-backed shared records instead of local-only runtime truth;
- drivers and immutable driver versions;
- triggers, deliveries, idempotency, retry, and concurrency controls;
- driver and task runs with leases, fencing, placement, and usage;
- connectors with explicit grants and append-only call audit;
- node-owned local effects with shared desired and observed state;
- a UI direction that presents interactive, background, event, and scheduled
  agents together;
- attribution fields that begin to connect a run to the agent-like record that
  caused it.

These are the right ingredients for a distributed AI SDLC control plane.

### What needs correction

The current model mixes four independent concerns:

1. `Agent` is a role-backed persona and also contains runtime intent.
2. `AgentService` is a behavior identity, trigger target, permission container,
   and desired-state service record.
3. `TriggerBinding` routes events but also carries behavior and authorization.
4. `DriverRun`, `TaskRun`, control-plane `AgentSession`, local filesystem
   sessions, and terminal sessions each represent overlapping notions of a run
   or session.

The claim that the workflow plane should supersede the role plane goes too far.
Shared execution plumbing should supersede duplicate runtime implementations.
It should not erase the organizational object that explains responsibility,
eligibility, escalation, and expected outcomes.

`AgentService` is also too deployment-shaped to be universal identity. An
event-driven GitHub remediation agent may execute only when a failed check
arrives; it still has identity and policy but has no continuously desired
process. Conversely, a lead terminal agent may require a supervised runtime.
The difference belongs in AgentDeployment, not in two kinds of agent identity.

### Directional verdict

Continue the shared Driver/Trigger/Run/TaskRun/Connector/Node execution work.
Before retiring the existing role-backed Agent plane, introduce the neutral
Agent, immutable revisions, separate PolicySet, canonical AgentRunIntent,
universal TranscriptEvent, and optional AgentDeployment. Then migrate both old
agent forms into that model.

## Architectural principles

1. **Responsibility defines an Agent.** Runtime duration and ingress type do not.
2. **Role describes; policy enforces.** Prompts and personas never grant authority.
3. **Definitions are immutable at execution time.** Every run pins revisions and
   digests needed to reproduce its effective configuration.
4. **External systems remain authoritative.** Loom stores normalized references,
   projections, relationships, and evidence.
5. **All ingress converges before admission.** Ready work and external events use
   different discovery mechanisms but the same policy and run contract.
6. **All runtimes emit one evidence vocabulary.** UI transport or provider-native
   formats are adapters, not canonical history.
7. **Local effects have one owner.** Shared state coordinates; a leased Node or
   TaskRun performs the effect and fencing rejects stale writers.
8. **Protected effects stop for humans.** Approval is a durable state transition,
   not a chat convention.
9. **No hidden cross-session memory.** Later work may explicitly retrieve durable
   evidence, but an Agent does not silently accumulate private memory.
10. **Migration is observable and reversible by phase.** Dual writes require
    reconciliation metrics and an explicit cutover flag.

## Logical architecture

Loom has four operating planes and a cross-cutting evidence ledger.

### 1. SDLC state plane

The state plane represents the work and delivery graph across systems:

- ideas, requirements, specifications, plans, epics, issues, and tasks;
- repositories, branches, changes, pull requests, and reviews;
- checks, builds, releases, deployments, incidents, alerts, and conversations;
- typed relationships such as decomposes-to, implements, reviews, blocks,
  deploys, caused-by, fixes, duplicates, and supersedes.

FleetDB issues remain the canonical ready-work source for Loom-managed work.
Provider-native objects remain canonical in their providers. `SDLCObjectRef`
gives Loom a stable identity; `SDLCProjection` stores only normalized fields
needed for routing, policy, UI, and analytics; `SDLCRelationship` joins the
graph without copying provider ownership.

An external reference is uniquely identified by:

```text
(workspace, provider, provider_scope, object_kind, external_id)
```

Every projection records the provider version or ETag where available,
observed time, and freshness. Writes use connector calls with provider
preconditions. Loom never assumes a cached projection is current enough for a
destructive action.

### 2. Organization and policy plane

The organization plane separates identity and responsibility from enforcement:

```text
Organization -> Workspace -> PrincipalRef / membership
      |
      +--------------------> organization PolicySet

Role --versioned-by--> RoleRevision
  |                       |
  +-- assigned-to ------> Agent --versioned-by--> AgentRevision
                                               |
PolicySet --versioned-by--> PolicyRevision -----+
```

#### Role

A Role names a job, responsibility, expected outcomes, escalation expectations,
and default eligibility. It may recommend skills, harnesses, prompts, and
models. It is understandable to an organization independent of a runtime.

#### Agent and AgentRevision

Agent is a durable, workspace-scoped identity. Names can change; IDs do not.
AgentRevision is immutable and contains the resolved definition used for a run:

- RoleRevision and explicit responsibility statement;
- persona reference;
- behavior reference: prompt/harness for direct agents, DriverVersion, or a
  FlowRevision entrypoint;
- PolicyRevision references and narrower agent restrictions;
- model/provider preferences and fallback policy;
- skill, tool, prompt, harness, and runtime adapter references with versions or
  content digests;
- eligible SDLC object types/scopes;
- provenance, creator, timestamp, and superseded revision.

Editing an agent creates a revision. It does not mint a new Agent. A running or
completed run never changes revisions retroactively.

#### PolicySet and effective policy

Policy is evaluated in layers:

```text
organization
  -> workspace/project
    -> role defaults
      -> agent restrictions
        -> run-scoped capability grant
```

By default, lower layers may only narrow higher layers. Widening requires an
explicitly authorized grant with scope, issuer, reason, and expiry. Deny wins.
Admission stores an EffectivePolicySnapshot digest on the run; relevant
expanded rules are retained so later audit is not dependent on mutable policy.

Policy governs:

- eligible work types, labels, priorities, repositories, environments, and data;
- allowed and denied tools and connector actions;
- model/provider choices and reasoning/effort ceilings;
- monetary, token, time, concurrency, and retry budgets;
- sandbox, filesystem, network egress, and data-retention boundaries;
- secret classes and access conditions;
- delegation and escalation limits;
- required approvals and eligible approvers.

Organization is the highest tenant/governance scope. Workspaces belong to one
Organization. Human and service approvers are recorded through stable
PrincipalRefs and membership/authority snapshots so an old decision remains
attributable after identity-provider membership changes.

Enforcement occurs at admission and again at the point of effect. A run admitted
to inspect a pull request is not thereby authorized to merge it.

### 3. Orchestration plane

The orchestration plane normalizes ingress and coordinates actors.

#### Unified ingress

```text
FleetDB ready frontier -> pull scheduler -------\
provider webhook ------> normalized event ------+
cron/manual command -----------------------------+--> AgentRunIntent
terminal/Flue/Slack conversation ----------------+
agent delegation --------------------------------/
```

`AgentRunIntent` is created before execution and contains:

- target Agent or Flow and requested revision/channel;
- source kind and normalized TriggerEvent or conversation reference;
- related SDLC object references and an optional ready-work candidate;
- requested action/capabilities;
- idempotency and concurrency subject keys;
- initiator, delegator, parent run, and trace context;
- admission deadline, priority, and budget request.

The admission service resolves revisions, evaluates policy, checks approvals or
creates ApprovalRequests, selects concurrency behavior, and either rejects,
queues, or admits a run.

#### Ready work is pull-based

FleetDB's ready frontier, dependency graph, status, priority, claim, and lease
rules remain authoritative. A task change event may wake the scheduler, but it
must not directly synthesize a run from `status=open`. The scheduler re-queries
the canonical frontier and atomically claims eligible work before admission.
This preserves Kanban semantics and prevents a replayed journal event from
becoming an independent queue.

#### External events are event-driven

GitHub checks, pull-request comments, Slack messages, alerts, and deployment
signals do not need to become Kanban issues before routing. A connector verifies
and normalizes the event; subscriptions fan it out to Agents or Flows; each leg
creates an idempotent AgentRunIntent. If durable planned work is needed, the
agent or flow may create or relate an Issue under policy.

#### Flow and delegation

A FlowRevision defines a graph of:

- agent steps, which create bounded delegations and child AgentRuns;
- deterministic Driver steps;
- waits, approval gates, joins, retries, and compensation steps;
- inputs, outputs, artifacts, and transition conditions.

Agent steps retain the participating Agent's identity and policy. The Flow does
not impersonate them. Delegation records who requested what, constraints,
expected result, deadline, budget, accepted revision, and completion outcome.

### 4. Execution plane

The execution plane runs admitted work without becoming the source of
organizational identity.

#### Driver and DriverVersion

Driver is an executable package definition. DriverVersion is immutable and
validated, with source/bundle digests and trust metadata. Drivers can implement
direct agent behavior, deterministic Flow steps, or task runners. A Driver may
be replaced without changing the Agent responsible for the work.

#### AgentRun, FlowRun, and TaskRun

- `AgentRun` is one bounded attempt by one AgentRevision under one effective
  policy snapshot.
- `FlowRun` is one bounded execution of one FlowRevision.
- `TaskRun` is a finite, leased/fenced execution against one task. It is the
  only run record allowed to commit the task completion transition.

A direct coding AgentRun may own a TaskRun. A FlowRun may contain deterministic
Driver steps and delegated child AgentRuns; those AgentRuns may own TaskRuns.
This preserves accountability while sharing execution machinery.

The current `DriverRun` can back the first AgentRun/FlowRun projection during
migration, but product APIs should stop making Driver the top-level identity.

#### AgentDeployment

Only agents needing a persistent runtime have an AgentDeployment. Examples are
an interactive project lead, an always-on Slack monitor, or a resident incident
coordinator. AgentDeployment records:

- Agent and selected AgentRevision/channel;
- desired state, instance count, placement, restart, drain, and update policy;
- observed instances, health, Node, leases, and runtime adapter;
- terminal/chat attachment capabilities;
- deployment-scoped resource limits.

An event-only GitHub remediation agent has Agent and AgentSubscription records
but no deployment. A schedule is a subscription, not proof of a process.

#### Example compositions

| Product concept | Identity and responsibility | Activation | Deployment/session/run shape |
|---|---|---|---|
| Project lead terminal | Agent with project-lead Role | Human conversation/manual work | AgentDeployment + AgentSession; bounded AgentRuns inside the session |
| GitHub CI remediation agent | Agent with CI-remediation Role | GitHub check-failure subscription | No deployment required; one AgentRun per admitted event, with optional TaskRun |
| Slack monitoring/delegation agent | Agent with support/on-call Role | Slack events and/or resident monitoring | Optional AgentDeployment; AgentSession per thread/incident; explicit Delegations to other Agents |
| Kanban implementation agent | Agent with implementer Role | Canonical ready-frontier pull | Usually no deployment; claimed issue -> AgentRun -> fenced TaskRun |
| Release coordination | FlowRevision involving release/review/operations Agents | Release candidate or manual trigger | FlowRun with child AgentRuns and mandatory deploy approval; the Flow is not another Agent |

#### Node, lease, and side-effect ownership

Nodes advertise capacity and capabilities. Shared controllers assign work; the
leased Node owns process, sandbox, filesystem, PTY, and local repository effects.
Fencing tokens reject completion, logs, or state changes from expired owners.
Controller commands are idempotent and desired state is distinct from observed
state.

## Cross-cutting evidence and learning contract

### Session versus run

An AgentSession groups bounded continuity: a terminal conversation, Flue chat
thread, Slack thread, incident-room interaction, or similar context. It can
contain multiple AgentRuns. AgentRun is the attempt and policy boundary.
TerminalSession is only a PTY transport attachment.

Background runs can exist without a conversational session. If a provider gives
useful continuity, Loom can create an AgentSession for it; session absence must
not prevent transcript collection.

### Universal TranscriptEvent

Every runtime adapter converts provider-native output into the same ordered,
append-only envelope. At minimum it carries:

- event ID, monotonic sequence, event time, observed time, and schema version;
- workspace, Agent, AgentRevision, AgentSession, AgentRun/FlowRun/TaskRun, parent
  run, delegator, trace ID, and span ID;
- event type and structured content parts;
- source event and SDLC object references;
- tool name/version, call ID, sanitized input, result, duration, and decision;
- policy evaluation, capability grant, ApprovalRequest/Decision, and effect;
- provider/model, token usage, estimated/actual cost, latency, and retry data;
- prompt, harness, skill, driver, tool, and runtime adapter versions/digests;
- artifact references, state transitions, summary, outcome, and error class;
- redaction classification, retention class, and integrity/provenance metadata.

Canonical event types include session/run lifecycle, user/agent message, tool
call/result, delegation, policy decision, approval requested/decided, artifact,
usage, state transition, external effect, summary, and error.

Loom does not store hidden chain-of-thought. Adapters store provider-safe
reasoning summaries when available, explicit decisions, tool evidence, and
outcomes. Raw provider payloads may be retained as access-controlled artifacts
when policy permits; they are not the portable query model.

OpenTelemetry GenAI semantic conventions are an export vocabulary, not the
FleetDB schema. Loom owns a stable canonical event version and maps it to traces,
logs, and metrics.

### Analytics dimensions

The normalized contract enables comparison by AgentRevision, RoleRevision,
PolicyRevision, model, provider, prompt, harness, skill set, DriverVersion, tool
version, ingress, SDLC object type, environment, and outcome. Evaluation and
feedback records should reference immutable runs and revisions rather than
mutating them.

## Human approval architecture

Human approval is mandatory by default for:

| Protected action class | Approval attaches to |
|---|---|
| Merge | exact repository, pull request, and expected head/base revisions |
| Production deploy | release/artifact digest, environment, and deployment plan |
| Rollback | environment, target revision, impact scope, and rollback plan |
| Incident communication | audience/channel and proposed message or bounded template |
| Destructive infrastructure change | resource selection, command/plan digest, and blast radius |
| Secret access | secret class/name scope, purpose, grantee, and duration; never secret value |
| Budget escalation | previous limit, requested limit, run/delegation scope, and expiry |

An approval is action- and revision-specific, has an expiry, and is consumed or
revalidated at the point of effect. Material changes to the subject invalidate
it. Approvers cannot approve outside their own policy authority. The transcript
and action ledger record both denied and granted attempts.

Emergency or break-glass operation is a separate explicit policy with stronger
authentication, a reason, short expiry, mandatory audit, and post-action review;
it is not an implicit bypass.

## Product/API projections

The primary product resources should be:

- `/agents`, `/agents/{id}/revisions`, `/agents/{id}/subscriptions`, and optional
  `/agents/{id}/deployments`;
- `/roles` and `/policy-sets` with immutable revision endpoints;
- `/flows` and `/flows/{id}/revisions`;
- `/run-intents`, `/agent-runs`, `/flow-runs`, and `/task-runs`;
- `/sessions/{id}/transcript-events` and `/artifacts`;
- `/approvals`, `/delegations`, and `/messages`;
- `/organizations`, `/principal-refs`, and membership/authority projections;
- `/sdlc-objects` and relationship/projection endpoints.

The UI presents Agents and Flows as separate first-class concepts. Driver,
binding, lease, and placement details remain available in advanced/operator
views. Agent screens may show interaction modes—interactive, background,
event-driven, scheduled—but those are capabilities/projections, not identities.

## Ownership boundaries

| Concern | Source of truth |
|---|---|
| Agent, Role, revisions, policy, run intent, approvals, transcript index | FleetDB through Loom control-plane APIs |
| Human/service identity authentication and group membership | Configured identity provider; Loom stores PrincipalRefs and decision-time authority evidence |
| Ready-work graph and Loom task status | FleetDB |
| GitHub pull request/check/branch truth | GitHub; Loom keeps refs/projections |
| Slack channel/thread/message truth | Slack; Loom keeps refs/projections |
| Build/deploy/incident/monitoring native state | The provider; Loom keeps refs/projections |
| Local process, sandbox, PTY, worktree, and filesystem effects | The leased Node, reporting observed state |
| Provider-native raw transcript | Provider or protected artifact store; Loom keeps canonical TranscriptEvents |
| Cross-session memory | None |

## Non-negotiable invariants

1. A run always names exactly one immutable AgentRevision or FlowRevision.
2. A completed run retains the effective policy digest and executable/config
   version references used at admission.
3. TriggerBinding/AgentSubscription never defines identity or grants authority.
4. An Agent does not need an AgentDeployment to exist or run.
5. Only a valid leased/fenced TaskRun commits task completion.
6. External destructive effects re-check source freshness and policy immediately
   before execution.
7. Every protected action has a matching valid human ApprovalDecision.
8. Every runtime emits canonical TranscriptEvents, including denied actions.
9. A terminal transport failure cannot erase the canonical transcript.
10. No model, prompt, persona, or skill can widen policy.
11. No cross-session memory is read or written implicitly.
12. Dual-written migration records reconcile to zero unexplained divergence
    before a read-path cutover.

## Explicit non-goals

- Mirroring every provider field or replacing GitHub, Slack, CI, deployment, or
  incident-management authority.
- Treating every webhook, cron job, script, or workflow step as an Agent.
- Making one universal runtime process shape mandatory for all agents.
- Persisting hidden model reasoning or introducing autonomous cross-session
  memory.
- Encoding organizational authority in UI labels, prompts, or connector secrets.
- Requiring event-driven operational work to become a Kanban issue first.

## Relationship to existing proposals

This proposal preserves the shared execution, trigger, connector, task-run,
lease, and distributed-control-plane investments in:

- [FleetDB Agent Platform V2](fleetdb-agent-platform-v2-proposal.md);
- [Distributed Control Plane](distributed-control-plane.md);
- [Unified Agent UX](2026-07-01-unified-agent-ux-proposal.md);
- [Agent Identity Record](2026-07-07-agent-identity-record.md).

It supersedes their end-state claims where they make `AgentService` the
universal identity, make a trigger binding the owner of authority, treat a
workflow plane as a replacement for Role, or conflate session continuity with
an execution attempt. The older documents remain valuable implementation and
historical context.

## External design references

These references influenced interoperability vocabulary; Loom's schema remains
its own stable contract.

- [GitLab Duo Agent Platform: agents, flows, triggers, and sessions](https://docs.gitlab.com/user/get_started/get_started_agent_platform/)
- [Slack agent concepts and workflow distinction](https://docs.slack.dev/ai/agents/)
- [GitHub coding-agent entry points](https://docs.github.com/en/copilot/concepts/agents/about-third-party-coding-agents)
- [A2A Protocol: Agent Card, Task, Message, Artifact, and Context](https://a2a-protocol.org/dev/specification/)
- [CloudEvents specification](https://github.com/cloudevents/spec/blob/main/cloudevents/spec.md)
- [OpenTelemetry generative AI attributes](https://opentelemetry.io/docs/specs/semconv/registry/attributes/gen-ai/)
