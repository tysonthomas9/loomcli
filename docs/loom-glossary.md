# Loom Glossary and Concept Map

**Status:** Canonical vocabulary

**Last updated:** 2026-07-09

**Applies to:** Loom, FleetDB, harness adapters, desktop/UI surfaces, and agent runtimes

This document is Loom's shared dictionary. Ordinary meanings, external
framework terminology, and older Loom documents are not authoritative for the
Loom control-plane model when they conflict with this glossary. External
framework terms remain authoritative inside their own adapter boundary.

The architecture decision behind these definitions is
[ADR-0001](adr/0001-agent-identity-role-policy-flow-and-deployment.md).

## Loom in one sentence

Loom is a harness-neutral AI SDLC control plane: it governs agents across
ideation, planning, implementation, review, deployment, and post-deployment
operations while FleetDB retains the canonical identity, policy, execution,
session, transcript, approval, artifact, and SDLC records.

Loom is not the Loom video product and is unrelated to Java Project Loom.

## The four operating planes

| Plane | Question it answers | Canonical Loom/FleetDB objects |
|---|---|---|
| SDLC state plane | What product and delivery state exists? | `SDLCObjectRef`, `SDLCProjection`, `SDLCRelationship`, issues, dependencies |
| Organization and policy plane | Who is responsible, and what may they do? | `Role`, `Agent`, revisions, `PolicySet`, approvals |
| Orchestration plane | Why did work start, and how is it coordinated? | `AgentSubscription`, `TriggerEvent`, `ExecutionIntent`, `Workflow`, `Delegation` |
| Execution plane | Which harness performed the work, where, and with what result? | `Execution`, `ExecutionAttempt`, `AgentSession`, `RuntimeDeployment`, `TaskRun`, `Node` |

Evidence and learning are cross-cutting. `TranscriptEvent`, `Artifact`,
`AgentEvaluation`, traces, and audit records describe what happened in every
plane. Analytics may propose changes, but never silently change identity,
policy, or an immutable revision.

## Authority rule

Loom and FleetDB own the canonical, public data formats for:

- organizational Agents and Roles;
- policy, grants, approvals, and budgets;
- sessions and external conversation/thread relationships;
- execution intent, execution, attempts, status, lineage, and usage;
- transcript events, artifacts, evaluations, and audit;
- normalized SDLC references, projections, and relationships.

Harnesses may require private state for recovery, such as a Flue session tree,
submission journal, or a CLI resume token. FleetDB stores that state through a
versioned adapter record or opaque checkpoint. The harness adapter owns how the
private payload is interpreted; it does not become the product data model or
the authority for Loom identity, lifecycle, history, or policy.

## Responsibility and identity

| Term | Meaning | It is not |
|---|---|---|
| **Role** | A named organizational responsibility, expected outcomes, and default work eligibility. Examples: project lead, planner, implementer, reviewer, incident commander. | A running process, a harness profile, or a complete security policy. |
| **RoleRevision** | An immutable version of a Role definition. | Mutable history on the Role row. |
| **Persona** | Interaction style, tone, and presentation defaults. | Authority, policy, harness identity, or Agent identity. |
| **Agent** | A durable Loom actor accountable for a Role or responsibility and governed independently. An Agent can use any supported harness. | A webhook, workflow, terminal, model, process, harness definition, or harness instance. |
| **AgentRevision** | An immutable resolved control-plane definition: RoleRevision, responsibility, persona, PolicyRevision references, implementation reference, and normalized component inventory. Every Execution pins one revision. | A copy of an external framework's complete configuration schema. |
| **AgentImplementationRef** | The immutable binding from an AgentRevision to a harness artifact and definition/entrypoint, such as a Flue agent module, Codex CLI profile, Claude SDK agent, or future Loom-native harness definition. | The Agent's organizational identity. |

An actor is a Loom Agent when it has all of the following:

1. a durable Loom identity;
2. a defined responsibility and expected outcome;
3. an independently governable authority boundary;
4. attributable Executions, decisions, and evidence.

A GitHub CI remediation actor or Slack monitoring/delegation actor can therefore
be a Loom Agent. The webhook or channel event is only activation. A fixed
sequence with no independent responsibility is a Workflow or automation.

An external harness may use the word *agent* differently. For example, Flue
defines an agent as an LLM inside a harness and distinguishes AgentDefinition,
AgentInstance, Harness, Session, Operation, and Turn. Those are runtime objects
mapped by the Flue adapter; they are not automatically new Loom Agent identities.

## Governance

| Term | Meaning |
|---|---|
| **Organization** | The tenant and highest Loom governance boundary. It owns workspaces, principals/memberships, organization PolicySets, and approval authority. |
| **PrincipalRef** | Stable reference to a human, service, or group identity from Loom or an external identity provider. |
| **PolicySet** | A versioned collection of enforceable rules for work eligibility, tools/connectors, repository/environment/data scope, models, budgets, concurrency, secrets, sandboxing, egress, authority, escalation, and approvals. |
| **PolicyRevision** | An immutable version of a PolicySet. |
| **EffectivePolicySnapshot** | Resolved policy pinned to an Execution after organization, workspace/project, role, agent, and execution-grant layers are combined. |
| **CapabilityGrant** | A narrow, time- or execution-bounded authorization to perform an action on a resource. |
| **ApprovalRequest** | A durable request for a human decision before a protected action. |
| **ApprovalDecision** | The approver, decision, exact scope/version, conditions, reason, and expiry associated with an ApprovalRequest. |

Role describes responsibility. PolicySet enforces authority. A prompt, persona,
skill, harness definition, or external framework configuration never widens
Loom policy.

Human approval is required by default for merge, production deployment,
rollback, incident communication, destructive infrastructure changes, secret
access, and budget escalation.

## Harness and implementation boundary

| Term | Meaning | It is not |
|---|---|---|
| **Harness** | The runtime system surrounding a model: context, sessions, tools, skills, model loop, sandbox access, recovery, and runtime events. | The Loom control plane or organizational Agent. |
| **HarnessAdapter** | Stable Loom identity for an integration that registers artifacts, controls work, maps native sessions/executions, imports events, reports outcomes, and stores checkpoints. | A new canonical data model per harness. |
| **HarnessAdapterRevision** | Immutable adapter implementation/mapping contract, including supported native versions, capabilities, mapping schema, code digest, and compatibility tests. Every ExecutionAttempt pins one. | A mutable plugin version string. |
| **HarnessArtifact** | Stable registered implementation package identity. | A running deployment or Agent identity. |
| **HarnessArtifactRevision** | Immutable built artifact plus framework version, compatible HarnessAdapterRevision, source/bundle digests, and normalized manifest projection. Existing `DriverVersion` is the initial storage form. | Loom-authored copies of every harness source field. |
| **ComponentRef** | Normalized version/digest reference used for analytics, such as model, prompt, instructions, skill, tool, harness, adapter, or sandbox. | Authority to execute that component. |
| **RuntimeDeployment** | Desired/observed deployment of a harness artifact, such as a Flue server containing several agent and workflow modules or a future Loom harness service. | Necessarily one Agent. |
| **AgentDeployment** | Optional desired/observed binding for a dedicated or resident Loom Agent instance, such as an interactive lead daemon. It may use a shared RuntimeDeployment. | The universal deployment unit for event-only Agents. |
| **RuntimeSessionBinding** | Mapping from a canonical AgentSession to harness-native instance, harness, session, thread, or resume identifiers. | Canonical session identity. |
| **RuntimeCheckpoint** | Versioned opaque/private harness state retained for recovery under a canonical session/execution/attempt. | A public Loom transcript or portable cross-harness memory. |

Loom is intentionally harness-neutral. The first full framework adapter is
Flue. Expected later adapters include Vercel eve and a future Loom-native
harness. Claude Code, Codex, and other CLIs or SDKs can use direct adapters even
when they do not expose a full framework manifest.

## Coordination and activation

| Term | Meaning | It is not |
|---|---|---|
| **Workflow** | A registered finite orchestration/automation identity. A WorkflowRevision references an immutable harness artifact definition or Loom-native implementation. | A responsible Agent or a universal graph schema copied from a harness. |
| **WorkflowRevision** | Immutable Loom registration of a finite workflow implementation, input/output contract projection, and implementation reference. | Ownership of the framework's private workflow representation. |
| **Trigger** | A condition or external signal that may initiate work. | An Agent. |
| **AgentSubscription** | A durable routing rule from an event, schedule, ready-work class, conversation, or manual command to an Agent or Workflow. | Agent identity or authority. |
| **TriggerEvent** | A normalized immutable occurrence received from an external or internal source. | An Execution or mutable provider projection. |
| **ExecutionIntent** | A durable request before admission. It carries target, source context, requested capabilities, deduplication/concurrency keys, and policy inputs. | Proof that execution was admitted or started. |
| **Delegation** | A durable transfer/request of bounded work from one Loom Agent/Execution to another. | Every harness-internal subagent call. |
| **AgentMessage** | A durable addressed message among Agents or principals when delivery state matters. | The ordered transcript of an Execution or Session. |

Use this test when naming a concept:

- **Agent** answers *who is responsible and governable?*
- **Workflow** answers *what finite coordination is registered?*
- **HarnessArtifactRevision** answers *which immutable implementation was built?*
- **HarnessAdapter** answers *how does Loom control and observe that runtime?*
- **Trigger/Subscription** answers *why and when may work start?*

## Execution, session, and evidence

| Term | Meaning | It is not |
|---|---|---|
| **Execution** | One admitted, attributable unit of work under an AgentRevision or WorkflowRevision and EffectivePolicySnapshot. Kinds are semantic (`interaction`, `workflow`, `task`, `action`), never harness names. | Necessarily a Flue workflow run, CLI process, or chat session. |
| **ExecutionAttempt** | One runtime ownership attempt for an Execution, with HarnessAdapterRevision, deployment/Node, lease/fencing, native correlation IDs, checkpoint, timing, and outcome. Retries/resumes create or transfer attempts according to adapter semantics. | A second business request or Agent identity. |
| **TaskRun** | Existing specialized finite execution against a Loom task, protected by lease/fencing and authorized to commit task completion. It links to its parent Execution. | A continuing Agent identity. |
| **AgentSession** | FleetDB-canonical continuity/context container for one Loom Agent, such as a terminal conversation, provider thread, ticket, or incident context. It contains zero or more Executions. | A harness-private session record or cross-session memory. |
| **TerminalSession** | A PTY transport attachment to an AgentSession or active ExecutionAttempt. | Canonical history, identity, or policy. |
| **TranscriptEvent** | FleetDB-canonical ordered append-only evidence event. Harness adapters normalize native events into this schema while retaining native type/version/cursor provenance. | A demand that every harness emit the same native wire format. |
| **Artifact** | Durable output reference such as a patch, diff, plan, report, build, log bundle, checkpoint, or deployment evidence. | Agent identity or an inline transcript payload. |
| **AgentEvaluation** | Immutable assessment of an Execution and pinned revisions/components. | A mutation of historical evidence. |
| **Node** | Runtime capacity that owns local effects and reports observed state. | The shared control plane. |
| **Lease** | Time-bounded ownership claim with fencing to reject stale writers. | Identity or a capability grant. |

Execution kinds remain stable as harnesses change. Examples:

| Native activity | Canonical mapping |
|---|---|
| Flue direct prompt or `dispatch(...)` submission | `Execution(kind=interaction)`; Flue submission/operation IDs are native refs |
| Flue workflow invocation | `Execution(kind=workflow)`; Flue `runId` is a native ref |
| Codex or Claude CLI interactive prompt | `Execution(kind=interaction)` plus CLI session/process attempt refs |
| Codex or Claude CLI task invocation | `Execution(kind=task)` plus TaskRun when it owns a Loom task |
| Claude/OpenAI SDK call or agent loop | Appropriate semantic Execution kind; SDK trace/thread IDs are native refs |
| Vercel eve session or scheduled agent | Interaction or workflow Execution through the eve adapter |

Loom has no implicit cross-session Agent memory. An AgentSession may be resumed
and retain its own canonical history. A different session only receives prior
evidence through an explicit, policy-governed retrieval recorded in its
transcript.

## External SDLC state

| Term | Meaning |
|---|---|
| **SDLCObjectRef** | Stable Loom identity for an object whose authority may live in another system, keyed by provider, scope, kind, and external ID. |
| **SDLCProjection** | Latest normalized fields Loom needs for routing, policy, display, and analytics, with provider version/freshness. |
| **SDLCRelationship** | Typed link among SDLC objects, such as implements, reviews, deploys, caused-by, fixes, or supersedes. |
| **Connector** | Configured boundary to an external provider, with inbound or outbound integration metadata and credential references. |
| **ConnectorGrant** | Explicit action/resource authorization for connector egress, issued from effective policy to an Execution. |

GitHub, Slack, CI, deployment, incident, and observability systems remain
authoritative for their native objects. FleetDB is authoritative for Loom's
normalized references, projections, relationships, decisions, and evidence.

## Canonical request lifecycle

```text
ready-work pull ----\
external event ------+--> ExecutionIntent --> policy/admission --> Execution
conversation --------+
manual/schedule -----/
                                                   |
                                                   +--> ExecutionAttempt via HarnessAdapter
                                                   +--> TaskRun / tools / approvals
                                                   +--> TranscriptEvent / Artifact / SDLC updates
```

Ready-work events wake a scheduler; they do not replace FleetDB's canonical
ready-frontier selection. External events may route directly without becoming
Kanban cards. Both paths use the same Loom-owned admission and evidence formats.

## Product and repository names

| Name | Loom-specific meaning |
|---|---|
| **Loom** | The harness-neutral AI SDLC control plane and its CLI/server/desktop product. |
| **FleetDB** | Canonical shared data/control-state service used by Loom. It is not legacy fleet worker mode. |
| **fleet** | Older remote worker coordination/claiming terminology. Prefer `Node`, `ExecutionAttempt`, or `TaskRun`. |
| **Flue** | External open-source TypeScript Agent Harness Framework from `withastro/flue`. Loom pins and adapts Flue artifacts; Flue is not owned by Loom. |
| **Vercel eve** | External filesystem-first durable agent framework and anticipated harness adapter after Flue. |
| **Aether** | Loom UI/design-system and wireframe direction used by adjacent repositories. It is not an agent runtime. |
| **Codex** | OpenAI coding CLI or SDK used through a harness adapter. It is not the Loom control plane. |
| **Claude** | Anthropic coding CLI or SDK used through a harness adapter. It is not a Loom Role. |
| **Daytona** | Remote sandbox/runtime provider. It is not the control plane or harness identity. |
| **atlas**, **ember**, **falcon**, **nova** | Conventional Agent/worktree names, not architectural layers. |

## Legacy and transitional names

| Current name | Target interpretation |
|---|---|
| `Agent` / agentdef row | Migrates to canonical Agent identity; runtime, policy, and deployment fields split out. |
| `AgentService` | Split into Agent, AgentRevision, AgentSubscription, optional AgentDeployment, and policy. |
| `WorkerProfile` | Becomes ExecutionProfile: reusable runner, placement, and resource defaults. |
| `Driver` / `DriverVersion` | Initial storage/registration form for HarnessArtifact and immutable HarnessArtifactRevision. |
| `DriverRun` | Migrates to canonical Execution plus ExecutionAttempt/native refs; it remains a compatibility implementation record during cutover. |
| task-kind `AgentSession` | Execution-shaped legacy record; migrate attempt state to Execution/TaskRun and retain AgentSession only for continuity. |
| filesystem `SessionRecord` | Import into AgentSession, Execution, TranscriptEvent, and Artifact records. |
| `TriggerBinding` | Evolves into AgentSubscription/Workflow routing; identity and authority leave the binding. |

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

### Custom Prompt Template Variables

A prompt file is a Go `text/template`. Unlike the built-in planning and task
prompts — which get a workspace table, an epic-scope line, safety rules and a
prior-attempt checkpoint spliced in for them — nothing is added to a custom
prompt. These variables are how a custom prompt asks for those same pieces, one
at a time. Referencing none of them leaves the file rendered exactly as written.

| Variable | Contents |
|---|---|
| `{{.AgentName}}` | Agent (worktree) name this run was launched for. |
| `{{.WorktreeName}}` | Same value as `AgentName`. |
| `{{.Role}}` | The real role name the daemon spawned the agent under; `custom` for a hand-run `loom agent`. |
| `{{.TaskID}}` | The task the daemon pre-claimed for this run. Empty in one-shot and auto mode, where the agent claims its own task mid-turn. |
| `{{.EpicID}}` | The epic the agent is scoped to (`--parent`), or empty. |
| `{{.WorkspaceBlock}}` | Multi-repo workspace section: the repo/path/branch table plus the "run git here, run `loom data` there" rules. Empty outside workspace mode. |
| `{{.EpicScope}}` | The "only select tasks from this epic" instruction the built-in prompts use. Empty when `EpicID` is empty. |
| `{{.SafetyBlock}}` | Shared multi-agent safety rules (do not stash, do not switch branches, do not clean up another agent's files). |
| `{{.CheckpointBlock}}` | "PREVIOUS ATTEMPT CONTEXT" for the last crashed or preempted attempt in this worktree. Empty when there is no checkpoint or a session resume is armed. |
| `{{.TaskDetail}}` | Full detail of `TaskID`: title, status, priority, labels, description, design, acceptance criteria, notes, dependencies. |

The last five are computed only when the template names them, so a prompt that
ignores `{{.TaskDetail}}` never pays for the issue-backend fetch it would need.
The detection reads the parsed template, so mentioning a variable name in prose
is not a reference; a template that renders the whole context wholesale with
`{{.}}` names nothing and therefore gets only the first five.

Read-only roles additionally get the read-only preamble prepended, once,
regardless of what the template references.

Interactive prompts loaded through `loom lead --prompt <file>` share the same
template but only receive `AgentName`, `WorktreeName` and `Role`; they get the
safety rules appended unconditionally.

## Isolation and Trust

These words are the most overloaded in the codebase, because several
isolation-*shaped* features are not isolation:

- **Sandbox**: In Loom this almost always means the L1 workflow-bundle
  container (`LOOM_DRIVER_SANDBOX=container`), which contains the DriverRun
  bundle only. The TaskRun leaf that runs the LLM and edits code is not
  containerized on any local path.
- **Trust level** (`trusted` / `untrusted`): An admission decision, not a
  confinement. Untrusted means Loom refuses to run something; it never bounds
  what a running process can reach.
- **`read_only` / `allowed_tools` / `denied_tools`**: Backend tool and approval
  policy. Real restrictions where the backend has a mechanism, a prompt
  preamble where it does not — never an OS boundary.

`docs/design/execution-isolation.md` has the three-level model, the per-level
mechanisms, the Daytona remote-isolation path, and an explicit list of what is
not isolation.

## Other Overloaded Names

## Related documents

- [AI SDLC Agent Control Plane Architecture](design/2026-07-09-ai-sdlc-agent-control-plane-architecture.md)
- [ADR-0001: Agent identity, policy, harnesses, and deployment](adr/0001-agent-identity-role-policy-flow-and-deployment.md)
- [Current data-model inventory and migration](design/2026-07-09-agent-data-model-inventory-and-migration.md)
