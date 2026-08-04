# Loom Glossary

> **Status:** Current · *audited 2026-07-23*

This repo uses several ordinary words as Loom-specific concepts. In this
codebase, `loom` means the AI-agent orchestration platform backed by fleet-db,
not the video product or Java Project Loom.

This file is a **dictionary**, not a concept map: it disambiguates overloaded
terms and pins each one to code. It does not contain a request-lifecycle
walkthrough or an architecture narrative — those live in `docs/arch/` and
`docs/design/`. Testing vocabulary lives in
[testing-terminology.md](testing-terminology.md).

Every entry cites `path:line` in this repo unless marked otherwise. When a
term means several things, the entry enumerates the senses; **prefer the
qualified form in new prose**.

## Core Objects

- **Workspace**: A fleet-db-scoped project container. Workspaces own repos,
  roles, agent definitions, issues, daemon profiles, and local runtime state.
  Identified by its **workspace key** — the control-plane identifier carried as
  `WorkspaceKey` on nearly every domain type (`internal/domain/workspace.go:27`).
  `entity.Workspace.ID` (`internal/entity/workspace.go:23`) and the web UI's
  `workspaceId` route param are the same value under older names; prefer
  "workspace key" in prose and reserve "workspace ID" for quoting those two APIs.
- **Role**: The reusable configuration for an agent: prompt selection, agent
  backend, model, task filter, tool policy, and role `kind`
  (`internal/domain/role.go:19-41`). Note `Role.Backend` is the **agent**
  backend — see **backend** below.
- **Agent**: A long-lived assignment of a Role to one or more repos within a
  workspace (`internal/domain/agent.go:35-42`). Worker agents are supervised by
  daemon/runtime loops; interactive agents are launched in terminal tabs.
  Explicitly *not* the same as a fleet-db Worker record — see **Worker**.
- **Lead**: The default interactive role and terminal agent. It is not the only
  possible interactive agent. Lead/orchestrator role names additionally opt
  into epic ownership and assignment delivery; `kind=interactive` alone does
  not grant those capabilities (`internal/epicrunner/start.go:71-79`).
- **Orchestrator**: Two senses that do **not** coincide.
  - *Role name*: `orchestrator` is one of exactly two literal strings (with
    `lead`) that grant epic ownership — matched by `epicrunner.IsLeadRole`
    (`internal/epicrunner/start.go:71-79`) and `domain.IsInteractiveRoleName`
    (`internal/domain/role.go:60-67`).
  - *Category*: `docs/product/orchestrator-worker-model.md:153` uses
    "orchestrator" for **any** `kind=interactive` agent. That superset does not
    gain epic ownership unless the role is literally named `lead` or
    `orchestrator`.
- **Worker**: Four unrelated things. Always qualify.
  - **Worker (role/agent)**: An autonomous role or agent that claims and
    completes tasks under daemon supervision; `role.kind=worker`
    (`internal/domain/role.go:12`).
  - **Worker (fleet-db record)**: fleet-db's per-claim row for one task claim.
    An Agent persists across many of them
    (`internal/domain/agent.go:35-37`).
  - **`loom worker`**: A remote worker *process* that registers with a control
    plane and executes tasks over HTTP — for containers and remote machines
    (`internal/cli/serve/worker/worker_cmd.go:40-50`). Configured by a
    **WorkerProfile**, a named role+backend+repos+limits template
    (`internal/domain/platform.go:104-119`).
  - **Task worker (`driver.TaskWorker`)**: An in-process claim loop inside
    `loom serve` that leases and runs queued **task runs**
    (`internal/driver/task_worker.go:18`). One goroutine per concurrency slot,
    started by `startDriverTaskWorkers` (`internal/cli/serve/serve.go:388-390`)
    and sized by `LOOM_DRIVER_TASK_WORKER_CONCURRENCY`
    (`internal/cli/serve/serve.go:48,462-463`). Not an agent, not a fleet-db
    row, not a separate process. Write "serve task worker".

  Role kind and **agent mode** are orthogonal axes: `AgentMode` is
  `ephemeral` or `service` (`internal/domain/control_plane.go:8-9`) and says
  nothing about `role.kind`.
- **Epic**: An issue of type `epic` (`internal/types/enums.go:55`) that groups
  child tasks. A lead's active epic is `agent.parent`; a lead owns at most one
  at a time, ownership survives the epic draining, and starting a second epic
  on a bound lead is rejected. Ownership changes are serialized by
  `epicrunner.AcquireBindLock` (`internal/epicrunner/start.go:82`); the product
  rule is `docs/product/lead-agent-epic-runner-spec.md:50-54`.
- **Stack**: A group of lineage-linked tasks in one repo whose PRs build on each
  other. The ID is conventionally `<kind>:<value>`, e.g. `epic:EPIC-1`
  (`internal/stacklineage/types.go:15-17,56-64`). Each member is a **unit**
  (`stacklineage.Node`, `internal/stacklineage/types.go:70`) with a parent
  pointer `BaseTaskID` and a stable branch `loom/stack/<stack>/<task>`
  (`internal/stacklineage/branch.go:10,18-27`). Driven by `loom stack`
  (`internal/cli/stack/stack_cmd.go:27`). "Stack" **never** means the running
  system in Loom prose — for that say "local-mode stack (processes)" or
  "deployment", and mark it.
- **Lineage**: Two senses. **Stack lineage** — the parent-pointer chain between
  stack units (`stacklineage.Node.BaseTaskID`). **Agent lineage** — the
  orchestrator→worker "spawned by" chain via `OrchestratorSessionID`
  (`docs/product/orchestrator-worker-model.md:54-56`).

## Role Kind

`role.kind` is the primary split between interactive and worker behavior:

- `interactive`: A human-in-the-loop terminal/orchestration agent. Opening its
  terminal runs the lead runtime, usually `loom lead`, optionally with
  `--prompt <prompt_file>`. Interactive agents are not daemon-supervised
  plan/task workers; they run when their terminal is opened.
- `worker`: An autonomous task-loop agent launched by `loom agent`, `loom plan`,
  or `loom task`, and supervised by the daemon/runtime for plan/task work.

When `kind` is unset, `domain.ResolveRoleKind` preserves the legacy naming
convention: roles named `lead` or `orchestrator` resolve to `interactive`; other
names resolve to `worker` (`internal/domain/role.go:46-57`).

Role kind selects runtime placement, not product authorization. In particular,
epic-runner ownership remains deliberately name-based through
`epicrunner.IsLeadRole` until Loom has an explicit capability/policy field.

## Prompt Selection

`prompt_file` selects the prompt for a role. For interactive roles it is passed
to `loom lead --prompt <prompt_file>` when the terminal opens
(`internal/cli/agent/lead/lead.go:80`).

Prompt values can be:

- A workspace or absolute file path, such as `prompts/reviewer.md`.
- A built-in prompt selector, `builtin:<id>` (`internal/cli/agent/prompts.go:376`).
  For example, `builtin:pr-review` selects the embedded PR-review terminal-agent
  prompt (`internal/domain/interactive_prompt.go:11-15`).
- Empty for the default built-in lead prompt.

## Deployment & Runtime

- **Local mode / cloud mode**: The deployment shape detected at bootstrap.
  Local = embedded fleet-db under `~/.loom`; cloud = external fleet-db via
  `LOOM_FLEET_DB_URL`. `bootstrap.DetectMode` is the only place the distinction
  is made (`internal/bootstrap/mode.go:26-58`). A healthy embedded runtime is
  reused, not respawned per invocation (`bootstrap.reuseEmbeddedRuntime`,
  `internal/bootstrap/embedded.go:183,221`).
- **Local-only workspace**: Unrelated to local mode — a workspace whose repo has
  no git remote (`rpc.WorkspaceInfo.LocalMode`,
  `internal/rpc/protocol_results.go:37`).
- **fleet-db**: The control-plane data service that stores Loom state. Spell it
  `fleet-db` in prose (older docs write `FleetDB`; same service). It lives in a
  **separate repository** — bare `internal/...` paths in fleet-db discussions do
  not resolve in this tree; qualify them as `(fleet-db repo)`.
- **fleet mode**: A separate `loom serve` toggle (`--fleet-mode` /
  `LOOM_FLEET_MODE`, **off** by default) enabling fleet coordination — stale
  detector, task claims, fleet worker API, JWT signing
  (`internal/cli/serve/serve.go:172`, `internal/cli/serve/serve.go:46`).
  Independent of whether fleet-db is embedded or remote.
- **Control plane**: Two layers — always name which. **fleet-db** is the
  control-plane *data service* (state, leases, claims). **`loom serve`** is the
  control-plane *service* above it: it holds the connector vault key, runs the
  driver executor and outbox dispatcher, and is what remote workers register
  with (`internal/connector/vault.go:25-28`,
  `internal/cli/serve/worker/worker_cmd.go:44-46`). Unqualified, "the control
  plane" means fleet-db + serve together. Only *one* plane is attested in this
  repo; there is no documented set of "four planes".
- **Node**: Three senses. **Control-plane node** — a machine that runs agents /
  task runs; carries runtime provider, capacity, drain state and heartbeat
  (`internal/domain/control_plane.go:39-54`). **Stack node** — one task's slot
  in a stack (`internal/stacklineage/types.go:70`). **Node.js** — the JS runtime
  for driver bundles (`flue-node`; sandbox image `docker.io/library/node:22-slim`,
  `internal/driver/sandbox/container.go:95` `DefaultSandboxImage`, overridable
  via `LOOM_DRIVER_SANDBOX_IMAGE`). Write "control-plane node" or "stack node"; never bare
  "node".

## Workflow Platform

- **Driver**: A named TypeScript program registered with Loom/fleet-db, carrying
  a `trust_level` that gates where it may execute
  (`internal/domain/platform.go:64-77`). Not a device driver.
- **Driver version**: One immutable built bundle of a driver; runs pin to a
  version (`internal/domain/platform.go:87-102`).
- **Driver run**: One execution request for a driver version, from API, CLI,
  trigger, or schedule (`internal/domain/platform.go:396-411`).
- **Task run**: One leased, fenced, auditable execution attempt against a single
  fleet-db task (`internal/domain/platform.go:498-505`).
- **Runner**: The process that executes a task run (`local-task-runner`,
  `daytona-task-runner` — `internal/driver/bundled_runner.go:20`); named by
  `TaskRun.Runner` / `RunnerEntrypoint`.
- **Connector**: The per-source control-plane object for one named integration —
  inbound webhook endpoint + signing secret, and the sealed outbound credential
  (`internal/domain/connector.go:76-81`). Credentials are envelope-encrypted;
  only serve holds the vault key `LOOM_CONNECTOR_VAULT_KEY`
  (`internal/connector/vault.go:25-28`).
- **Trigger binding**: The rule routing an inbound event to a driver run, by
  exact `RouteKey` plus dot-segmented glob `event_type_patterns` (`*` matches
  one segment, `{a,b}` alternatives). The grammar must stay in lockstep with
  fleet-db's router (`internal/domain/platform.go:214-231`,
  `internal/trigger/pattern.go:1-21`).
- **Await**: One durable "wait for event X" registration made by a suspended
  workflow run. The pattern must be subject-scoped (`eventType:subject`, exact
  match only, no globs) and the deadline is mandatory
  (`internal/domain/await.go:3-22`).
- **Outbox**: The server-side notification queue. Serve creates `OutboxRecord`s
  (kinds `leadAssignment`, `leadTaskMessage` —
  `internal/domain/outbox.go:73,77`) and a dispatcher drains due rows.
  `DedupeKey` makes creation idempotent; `Seq` is monotonic per workspace
  (`internal/domain/outbox.go:102-110`).
- **Agent inbox**: The per-agent delivery queue an outbox record lands in —
  queued → delivered | failed, with dedupe key, claim lease, and the
  `DeliveredThreadID` of the conversation it was delivered into
  (`internal/domain/control_plane.go:235-259`). Entry point
  `agentinbox.Enqueue` (`internal/agentinbox/message.go:29`).

## Other Overloaded Names

- **backend**: Three senses, always disambiguate.
  - **Agent backend** — the AI CLI a role/agent invokes (`claude`, `codex`,
    `cursor`, `gemini`, `opencode`); `domain.Role.Backend`
    (`internal/domain/role.go:28`), `domain.Agent.Backend`
    (`internal/domain/agent.go:47`). The accepted set is
    `webui/terminal.ValidBackends` (`internal/webui/terminal/session_command.go:13`);
    implementations are `internal/cli/backends/backend_{claude,codex,cursor,gemini,opencode}.go`.
    `internal/backendnames` pins only the two literals other packages branch on
    by name — `claude` and `codex` (`internal/backendnames/names.go:7-10`).
  - **Issue backend** — the pluggable issue-tracking data-access layer
    (`internal/backend/types.go:1`).
  - **fleet-db backend** — *which* fleet-db a `loom data` command talks to:
    server (`--server` / `LOOM_SERVER_URL`) or local embedded
    ([agents/issue-tracker.md](agents/issue-tracker.md)).

  `DaemonProfile` carries `IssueBackend` and `AgentBackend` as separate fields
  (`internal/domain/daemon_profile.go:20-21`).

  The ordinary server-side Go web/API layer is **none** of these. HTTP route
  registration and the services behind it in `internal/webui` — e.g. the file
  module wrapping `service.FileService`
  (`internal/webui/handlers/misc/module.go:30`) and the issue module
  coordinating `service.IssueService`
  (`internal/webui/handlers/issues/module.go:35`) — is not a "backend". In
  arch docs call it the "Go web/API layer" or "server routes and services".
- **session**: Five artifacts. Bare "session" should not appear in new prose.
  - **Agent session** — fleet-db control-plane row for one agent run; `kind` ∈
    `task` | `orchestration` | `terminal` | `maintenance` | `ad_hoc`
    (`internal/domain/control_plane.go:59-63,81-103`).
  - **Terminal session** — fleet-db row for one PTY/terminal, keyed by
    `TerminalID`, optionally linked to an agent session via `SessionID`
    (`internal/domain/control_plane.go:112-131`).
  - **Session record** — the local on-disk artifact for one agent run
    (transcript, tokens, cost), indexed in `sessions/index.jsonl`
    (`internal/sessions/types.go:28-30`).
  - **Provider session / thread id** — the *backend CLI's own* conversation id,
    stored as `lead_harness_session_id` (harness backends) or
    `codex_provider_thread_id` (codex). The resume key; never loom's own session
    id (`docs/design/2026-07-22-lead-conversation-resume.md:55-56`).
  - **Lead session** — the terminal + AI process backing an interactive agent
    (`docs/product/orchestrator-worker-model.md:155`).
- **transcript**: Three artifacts.
  - **Session transcript** — `agent_transcript.jsonl` in a session record,
    either `raw` (the backend's own stream) or `canonical` (a `transcript.Event`
    stream); an empty format means legacy, treated as raw
    (`internal/sessions/types.go:19-26`).
  - **Provider transcript** — the backend CLI's own conversation store, read per
    provider (codex rollout files: `internal/sessions/codex_rollout.go`).
  - **Terminal transcript** — `TerminalSession.TranscriptRef`, terminal
    scrollback (`internal/domain/control_plane.go:124`).
- **flue**: The external TypeScript framework (separate repo, pinned by
  `internal/workflows/FLUE_COMMIT`) that builds and runs Loom's workflow driver
  bundles — `flue build`, `@flue/runtime`
  (`internal/workflows/workflows.go:320,621,715`). Driver bundles built this way
  carry `NativeFlueSchemaVersion` and run under runner kind `flue-workflow`
  (`internal/driver/register.go:22,27`). **Not** an agent harness.
  `flue-local` / `flue-daytona` are separately provider-profile names
  (`internal/driver/task_scheduling.go:208`).
- **harness**: The `harness-wrapper` supervisor that runs a backend CLI under a
  PTY and classifies its exit status; loom wraps it in `RunWithRetry`
  (`internal/harness/retry.go:1-10,89`). Unrelated to flue.
- **fleet**: See **fleet-db** and **fleet mode** under Deployment & Runtime —
  they are independently toggled and must not be conflated.
- **aether**: Two senses. (1) The Loom UI **design system** used by the web UI
  (`internal/webui/frontend/src/styles/variables.css:9,28,268`;
  `docs/design/aether-wireframe-mapping.md`). (2) A **deployment/host name** —
  `aether-dev` is a running loom server that docs send agents at
  (`docs/agents/issue-tracker.md:9-10`,
  `docs/design/workspace-provider-refactor.md:17`). "aether-dev" is always sense
  (2).
- **codex / claude**: Agent backends (AI CLIs) invoked by Loom
  (`internal/backendnames/names.go:7-10`), *not* the vendors' hosted products.
  `claude` also names the CLI this repo's contributors run; disambiguate when it
  matters.
- **daytona**: A remote-sandbox runtime and credential provider for agent and
  task-run placement — `AgentRuntimeDaytona` /
  `RuntimeCredentialProviderDaytona` (`internal/localsettings/settings.go:28,30`),
  `SandboxPlacement.Provider = "daytona"`
  (`internal/driver/task_scheduling.go:216`), bundled entrypoint
  `daytona-task-runner` (`internal/driver/bundled_runner.go:20`). Not a Loom
  deployment.
- **atlas**: **Not a Loom concept.** It is a conventional demo/test agent name
  (alongside `nova`, `bolt`, `falcon`) in fixtures and specs —
  `internal/epicrunner/assignment_context_test.go:55`,
  `internal/webui/handlers/terminal/agent_session_test.go:827`,
  `docs/design/epic-runner-lead-control.md`. Earlier revisions of this glossary
  and of `AGENTS.md` listed it as a deployment/provider concept; that was wrong.
- **gate**: Overloaded across testing and the issue model. `make gate` is the
  quality gate; `gate` is also a non-work (Gas Town) issue type filtered out by
  `internal/cli/taskfilter.go:26` and a daemon RPC operation family
  (`internal/rpc/protocol.go:51-56`). Testing senses are enumerated in
  [testing-terminology.md](testing-terminology.md).
- **swarm**: Two unrelated senses. In Loom: the **Swarm view**, the UI route
  `/ws/:workspaceId/swarm` showing an orchestrator and its workers
  (`docs/product/orchestrator-worker-model.md:66`). In `internal/types`:
  `MolTypeSwarm`, a Gas Town molecule type (`internal/types/enums.go:169`).

## Inherited (non-Loom) vocabulary

`internal/types` carries a foreign issue vocabulary from another product
("Gas Town") for compatibility. It is not Loom terminology — do not use these
words in Loom prose.

- **Gas Town types** (`molecule`, `gate`, `convoy`, `merge-request`, `slot`,
  `agent`, `role`, `rig`, `event`, `message`): custom issue types with no
  built-in constants, enumerated at `internal/types/enums.go:59-61`. Only
  `bug` / `feature` / `task` / `epic` / `chore` are core work types
  (`internal/types/enums.go:52-57`).
- **`Issue.RoleType`** (`polecat` | `crew` | `witness` | `refinery` | `mayor` |
  `deacon`, `internal/types/issue.go:108`) is a Gas Town role and is
  **unrelated** to `domain.Role`.
- **`MolType`** (`swarm` | `patrol` | `work`, `internal/types/enums.go:167-171`)
  classifies a molecule. It surfaces on the HTTP API as `mol_type`.

## Related

- [README.md](README.md) — index of the whole `docs/` tree, with the per-doc
  status vocabulary
- [testing-terminology.md](testing-terminology.md) — testing vocabulary, trap
  words, and the mandatory terminology handshake
- [agents/domain.md](agents/domain.md) — which domain docs to read, and when
- [agents/issue-tracker.md](agents/issue-tracker.md) — `loom data` runbook and
  the fleet-db backend split
- [security.md](security.md) — auth modes, IPC, subprocess env policy
- [observability/README.md](observability/README.md) — tracing docs and their
  precedence
- [design/README.md](design/README.md) — dated decision records (this repo's ADR
  equivalent), indexed by subject with per-doc status
- [arch/README.md](arch/README.md) — as-built architecture of shipped subsystems
