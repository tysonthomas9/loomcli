# Loom Glossary

This repo uses several ordinary words as Loom-specific concepts. In this
codebase, `loom` means the AI-agent orchestration platform backed by fleet-db,
not the video product or Java Project Loom.

## Core Objects

- **Workspace**: A fleet-db-scoped project container. Workspaces own repos,
  roles, agent definitions, issues, daemon profiles, and local runtime state.
- **Role**: The reusable configuration for an agent: prompt selection, backend,
  model, task filter, tool policy, and role `kind`.
- **Agent**: A named assignment that uses a role. Worker agents are supervised
  by daemon/runtime loops; interactive agents are launched in terminal tabs.
- **Lead**: The default interactive role and terminal agent. It is not the only
  possible interactive agent. Lead/orchestrator role names additionally opt
  into epic ownership and assignment delivery; `kind=interactive` alone does
  not grant those capabilities.
- **Worker**: An autonomous role or agent intended to claim and complete tasks
  under daemon supervision.

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
names resolve to `worker`.

Role kind selects runtime placement, not product authorization. In particular,
epic-runner ownership remains deliberately name-based through
`epicrunner.IsLeadRole` until Loom has an explicit capability/policy field.

## Prompt Selection

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

## Blocked, Stuck, and Stage

Three of these words collided before any of them were written down. The issue
status literal is unchanged in all of the below — only the display name and the
concept boundary are settled here.

- **Blocked** (dependency sense): an issue that cannot start because issues it
  depends on are still open. `BlockedBadge` renders "Blocked by N issues" on
  Kanban cards, and bottleneck views derive from the same dependency edges.
  This sense keeps the word "Blocked".
- **Stuck** (agent-declared sense): a worker parked the task and declared a
  blocker of its own — a person or another system has to act. This is the issue
  status literal `blocked`, and it stays `blocked` in the API, in fleet-db, and
  in `PatchIssueRequest`. It reads **Stuck** on screen, which is where Journey
  names the stage. Nothing renames the dependency sense.
- **`task.stuck`** (`internal/events`, `TaskStuckData`): a daemon event for a
  task that failed repeatedly across consecutive auto-mode invocations and was
  skipped so the loop could make progress elsewhere. Same word, different
  trigger: the loop gave up, the agent declared nothing. It is not an issue
  status, and it does not set one.

The rate-limit circuit breaker emits `circuit.opened` / `circuit.closed`, not a
third kind of "blocked".

### Stage vs phase

- **Stage**: UI-local vocabulary, not a loom domain object. Nothing stores a
  stage; the Journey section on the issue detail panel derives them from the
  issue's event history — a stage is a contiguous run of that history with the
  same status and the same owner. Because it is derived from a bounded event
  window, a Journey shows the stages that window can support, not necessarily
  the whole life of the issue.
- **Phase**: a runtime concept with storage behind it — the log of one stretch
  of agent work, `planning` or `implementation`, written to
  `tasks/<id>/<phase>.log` and surfaced as its own tab. A phase is something an
  agent ran; a stage is something the issue was.

## Other Overloaded Names

- **fleet / fleet-db**: The control-plane data service that stores Loom state.
- **flue**: The external agent harness framework Loom uses to build and run
  TypeScript agents and workflows.
- **aether**: The Loom UI/design system terminology used by the web UI.
- **codex / claude**: AI backends or agent CLIs used by Loom, depending on
  context.
- **daytona / atlas**: Loom deployment or provider concepts when referenced in
  design docs or runtime configuration.
