# Loom Glossary and Concept Map

**Status:** Canonical vocabulary

**Last updated:** 2026-07-09

**Applies to:** Loom, FleetDB, Flue, the desktop/UI surfaces, and agent runtimes

This document is the shared dictionary for Loom. Ordinary meanings and older
Loom documents are not authoritative when they conflict with this glossary.
The architecture decision behind these definitions is
[ADR-0001](adr/0001-agent-identity-role-policy-flow-and-deployment.md).

## Loom in one sentence

Loom is an AI SDLC control plane: it coordinates governed AI agents across
ideation, planning, implementation, review, deployment, and post-deployment
operations while retaining a common evidence trail.

Loom is not the Loom video product and is unrelated to Java Project Loom.

## The four operating planes

| Plane | Question it answers | Canonical objects |
|---|---|---|
| SDLC state plane | What product and delivery state exists? | `SDLCObjectRef`, `SDLCProjection`, `SDLCRelationship`, FleetDB issues and dependencies |
| Organization and policy plane | Who is responsible, and what may they do? | `Role`, `Agent`, revisions, `PolicySet`, approvals |
| Orchestration plane | Why did work start, and how is it coordinated? | `AgentSubscription`, `TriggerEvent`, `AgentRunIntent`, `Flow`, `Delegation` |
| Execution plane | Where and how is work executed? | `Driver`, `AgentDeployment`, `AgentRun`, `TaskRun`, `Node`, leases |

Evidence and learning are cross-cutting, not a fifth authority plane.
`AgentSession`, `TranscriptEvent`, `Artifact`, evaluations, traces, and audit
records describe what happened in every plane. Analytics may learn from this
evidence, but may not silently change policy or identity.

## Canonical object model

### Responsibility and identity

| Term | Meaning | It is not |
|---|---|---|
| **Role** | A named organizational responsibility, expected outcomes, and default work eligibility. Examples: project lead, planner, implementer, reviewer, incident commander. | A running process or a complete security policy. |
| **RoleRevision** | An immutable version of a Role definition. | Mutable history on the Role row. |
| **Persona** | Interaction style, tone, and presentation defaults. | Authority, policy, or agent identity. |
| **Agent** | A durable, addressable actor accountable for a Role or responsibility. An agent may be interactive, event-driven, scheduled, delegated, or continuously deployed. | A webhook, cron expression, workflow graph, terminal, model, or process. |
| **AgentRevision** | An immutable, resolved agent definition: role revision, responsibility, persona, behavior references, policy references, model/harness/skill/prompt references, and their versions or digests. Every run pins one revision. | A new Agent identity each time configuration changes. |

An actor is an Agent when it has all of the following:

1. a durable identity;
2. a defined responsibility and expected outcome;
3. an independently governable authority boundary;
4. attributable runs, decisions, and evidence.

A GitHub CI remediation actor can therefore be a **GitHub agent**. The webhook
that wakes it is only a trigger. A Slack actor with a monitoring, communication,
or delegation responsibility can be a **Slack agent**. A fixed sequence with no
independent responsibility is a Flow or automation, not an Agent.

### Governance

| Term | Meaning |
|---|---|
| **Organization** | The tenant and highest Loom governance boundary. It owns workspaces, principals/memberships, organization PolicySets, and approval authority. |
| **PrincipalRef** | A stable reference to a human, service, or group identity from Loom or an external identity provider. It is used for ownership, grants, and approvals. |
| **PolicySet** | A versioned collection of enforceable rules for task eligibility, tools/connectors, repository/environment/data scope, models, budgets, concurrency, secrets, sandboxing, egress, authority, escalation, and approval requirements. |
| **PolicyRevision** | An immutable version of a PolicySet. |
| **EffectivePolicySnapshot** | The resolved policy pinned to a run after organization, workspace/project, role, agent, and run-grant layers are combined. It is identified by a digest and retained for audit. |
| **Capability grant** | A narrow, time- or run-bounded authorization to perform an action on a resource. |
| **ApprovalRequest** | A durable request for a human decision before a protected action. |
| **ApprovalDecision** | The approver, decision, scope, conditions, reason, and expiry associated with an ApprovalRequest. |

Role describes responsibility. PolicySet enforces authority. Role defaults may
reference policies, but enforcement is never inferred from a prompt or persona.

The following action classes require a human approval by default:

- merge;
- production deployment;
- rollback;
- incident communication;
- destructive infrastructure change;
- secret access;
- budget escalation.

### Behavior, coordination, and activation

| Term | Meaning | It is not |
|---|---|---|
| **Driver** | A named executable implementation package for a behavior or flow. | The accountable actor. |
| **DriverVersion** | An immutable, validated build of a Driver, identified by source and bundle digests. | Agent identity or policy. |
| **Flow** | A durable definition of how one or more agents and deterministic steps coordinate toward an outcome. | An agent persona. |
| **FlowRevision** | An immutable version of a Flow graph and its references. | A mutable workflow document. |
| **Trigger** | A condition or external signal that may initiate work. | An Agent. |
| **AgentSubscription** | A durable routing rule from an event, schedule, ready-work class, conversation, or manual command to an Agent or Flow. | Agent identity. |
| **TriggerEvent** | A normalized, immutable occurrence received from an external or internal source. | A run or a mutable projection of the source system. |
| **AgentRunIntent** | A durable request for work before admission. It carries source context, target, deduplication key, requested capability, and policy inputs. | Proof that execution was authorized or started. |
| **Delegation** | A durable transfer or request of bounded work from one agent/run to another, including constraints and expected result. | An implicit parent-child prompt convention. |
| **AgentMessage** | A durable addressed message among agents or principals when delivery state matters. If it requests bounded work, it is accompanied by a Delegation. | A substitute for the ordered transcript of a run/session. |

Use this test when naming a concept:

- **Agent** answers *who is responsible and governable?*
- **Flow** answers *how do actors and deterministic steps coordinate?*
- **Driver** answers *what executable implementation runs?*
- **Trigger/Subscription** answers *why and when may it start?*

### Runtime and continuity

| Term | Meaning | It is not |
|---|---|---|
| **AgentDeployment** | Desired and observed runtime lifecycle for an Agent that needs a persistent or interactive presence: placement, instance count, restart policy, leases, and health. | Universal Agent identity. Event-only agents need no deployment. |
| **AgentRun** | One bounded, attributable attempt by a specific AgentRevision under a pinned policy snapshot. | A chat thread or an always-on process. |
| **FlowRun** | One bounded execution of a FlowRevision, with agent and deterministic child steps. | The identity of its participating agents. |
| **TaskRun** | A finite execution unit against a task, protected by lease/fencing semantics. It is the only execution record authorized to complete that task. | A long-lived agent identity. |
| **AgentSession** | A bounded continuity/context container that may hold multiple AgentRuns, such as a terminal conversation, Flue chat thread, or incident room interaction. | Cross-session memory or a single process. |
| **TerminalSession** | A PTY transport attachment to an AgentSession or run. | Canonical history, identity, or policy. |
| **Node** | Runtime capacity that owns local effects and reports observed state. | The shared control plane. |
| **Lease** | A time-bounded ownership claim with fencing used to prevent stale writers. | A durable identity or permission grant. |

Loom has no cross-session Agent memory model. Context is session-local. Durable
transcripts, artifacts, decisions, and SDLC state can be retrieved as explicit
evidence in a later session, but retrieval is not hidden persistent memory.

### Evidence and external state

| Term | Meaning |
|---|---|
| **TranscriptEvent** | The canonical ordered, append-only event envelope emitted by every agent runtime. It covers messages, tool calls/results, policy and approval events, state changes, usage, artifacts, summaries, and errors. |
| **Artifact** | A durable reference to an output such as a patch, diff, plan, report, build, log bundle, or deployment evidence. |
| **SDLCObjectRef** | Stable Loom identity for an object whose authority may live in another system. Keyed by provider, scope, kind, and external ID. |
| **SDLCProjection** | The latest normalized fields Loom needs for routing, policy, display, and analytics, with source version and observation time. |
| **SDLCRelationship** | A typed link among SDLC objects, such as implements, reviews, deploys, caused-by, fixes, or supersedes. |
| **Connector** | A configured boundary to an external provider, with inbound verification and sealed outbound credentials. |
| **ConnectorGrant** | An explicit action/resource authorization for connector egress. In the target model it is issued from effective policy to an agent/run, not inferred from a trigger. |

GitHub, Slack, CI, deployment systems, incident systems, and observability
systems remain authoritative for their native objects. Loom stores normalized
references, projections, relationships, and evidence; it does not create a
second competing source of truth.

## Canonical request lifecycle

All ingress converges before execution:

```text
ready-work pull ----\
external event ------+--> AgentRunIntent --> policy/admission --> AgentRun or FlowRun
conversation --------+
manual/schedule -----/
                                              |
                                              +--> TaskRun / tool calls / approvals
                                              |
                                              +--> TranscriptEvent + Artifact + SDLC updates
```

Ready-work events wake a scheduler; they do not replace FleetDB's canonical
ready-frontier selection and priority semantics. External events may route
directly without becoming Kanban cards. Both paths still produce the same run,
policy, evidence, and audit objects.

## Product and repository names

| Name | Loom-specific meaning |
|---|---|
| **Loom** | The AI SDLC control plane and its CLI/server/desktop product. |
| **FleetDB** | The canonical shared data/control-state service used by Loom. It is not synonymous with legacy fleet worker mode. |
| **fleet** | In older code and commands, remote worker coordination and claiming. Use the more specific `Node`, `TaskRun`, or FleetDB term in new designs. |
| **Flue** | Loom's TypeScript workflow/runtime and build substrate. A Flue chat is an interaction surface; it does not define a separate kind of session evidence. |
| **Aether** | The Loom UI/design-system and wireframe direction used by adjacent repositories. It is not an agent runtime. |
| **Codex** | OpenAI's coding-agent CLI/backend when used as a Loom runtime. It is not the Loom control plane. |
| **Claude** | Anthropic's coding-agent CLI/backend when used as a Loom runtime. It is not a Loom Role. |
| **Daytona** | A remote sandbox/runtime provider that may host execution. It is not the control plane. |
| **atlas**, **ember**, **falcon**, **nova** | Conventional example agent or worktree names. They are identities/fixtures, not architectural layers unless a specific document explicitly says otherwise. |

## Legacy and transitional names

| Current name | Target interpretation |
|---|---|
| `Agent` / agentdef row | Migrates to the neutral canonical Agent identity; its runtime intent fields move to AgentDeployment or policy. |
| `AgentService` | Transitional mixed identity/runtime record. Split into Agent plus optional AgentDeployment and AgentSubscription. Do not use as the universal future identity. |
| `WorkerProfile` | Becomes an ExecutionProfile: reusable runner, placement, and resource defaults. |
| `DriverRun` | Existing orchestration execution record. It becomes or backs the initial AgentRun/FlowRun projection while implementation is migrated. |
| task-kind `AgentSession` | Transitional execution-instance meaning. AgentSession becomes continuity; TaskRun/AgentRun own execution attempts. |
| filesystem `SessionRecord` | Legacy local session/transcript authority to import into FleetDB-backed AgentSession, AgentRun, TranscriptEvent, and Artifact records. |
| `TriggerBinding` | Evolves into AgentSubscription or Flow trigger routing; identity, role, and permissions no longer live in the binding. |

## As-built role configuration

The canonical model above is the target vocabulary. These sections describe the
fields currently implemented in the codebase.

### Role kind (`role.kind`)

`role.kind` is the primary split between interactive and worker behavior:

- `interactive`: A human-in-the-loop terminal/orchestration agent. Opening its
  terminal runs the lead runtime, usually `loom lead`, optionally with
  `--prompt <prompt_file>`. Interactive agents are not daemon-supervised
  plan/task workers; they run when their terminal is opened.
- `worker`: An autonomous task-loop agent launched by `loom agent`, `loom plan`,
  or `loom task`, and supervised by the daemon/runtime for plan/task work.

When `kind` is unset, `domain.ResolveRoleKind` preserves the legacy naming
convention: roles named `lead` or `orchestrator` resolve to `interactive`; other
names resolve to `worker`.

Role kind selects runtime placement, not product authorization. In particular,
epic-runner ownership remains deliberately name-based through
`epicrunner.IsLeadRole` until Loom has an explicit capability/policy field.

### Prompt selection (`prompt_file`)

`prompt_file` selects the prompt for a role. For interactive roles it is passed
to `loom lead --prompt <prompt_file>` when the terminal opens.

Prompt values can be:

- A workspace or absolute file path, such as `prompts/reviewer.md`.
- A built-in prompt selector, `builtin:<id>`. For example,
  `builtin:pr-review` selects the embedded PR-review terminal-agent prompt.
- Empty for the default built-in lead prompt.

## Related documents

- [AI SDLC Agent Control Plane Architecture](design/2026-07-09-ai-sdlc-agent-control-plane-architecture.md)
- [ADR-0001: Agent identity, policy, flows, and deployment](adr/0001-agent-identity-role-policy-flow-and-deployment.md)
- [Current data-model inventory and migration](design/2026-07-09-agent-data-model-inventory-and-migration.md)
