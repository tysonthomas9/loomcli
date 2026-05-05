# Local Mode Product Spec

**Status:** Draft
**Date:** 2026-05-04
**Related:** `docs/product/agent-execution-prd.md`,
`docs/product/agent-run-ux-spec.md`,
`docs/design/agent-run-visibility-plan.md`

## Purpose

Local mode is the first shippable slice of visible agent execution. It
assumes one machine, one shared filesystem, one Loom server, and local
agent processes.

Local mode should not require containers or remote scheduling.

Local mode must still use FleetDB as the shared control plane. It is a
single-machine deployment of the distributed architecture, not a separate
storage mode.

## Product Promise

When a user starts a local agent, the UI shows it immediately, attaches it
to the task it claims, records the session, and preserves enough evidence
to debug the result.

## FleetDB Parity Requirement

Local mode and distributed mode must use the same persistence and API path
for agent-visible state:

- workspaces and repos
- roles and agent definitions
- daemon profile
- nodes and heartbeats
- agent sessions
- terminal sessions
- agent leases
- agent commands
- artifacts
- issues, comments, dependencies, labels, and mutation streams

The local stack may run every process on one machine, but Loom must talk to
FleetDB for these resources through the same `internal/infra/fleetdb`
client used by distributed mode.

Local mode must not introduce:

- a Loom-only `memstore` control-plane path
- a local JSON/file database for agent state
- a sidecar API that implements different behavior than FleetDB
- UI routes that read local runtime files instead of FleetDB-backed
  session/artifact records when FleetDB records exist

If FleetDB lacks an endpoint needed by local mode, the product fix is to
add that endpoint to FleetDB and keep the local dogfood test as a parity
test for distributed mode.

## Local Topology

The first shippable local mode should start this topology with one command:

| Process | Responsibility |
|---|---|
| Redis | Durable backing store for FleetDB. |
| FleetDB | Issue store and Loom control-plane API. |
| Loom serve | Web/API server for the workspace UI. |
| Loom daemon | Local supervisor that starts planner/coder agents. |
| Local backend command | Deterministic dogfood backend or real Codex CLI. |
| Web UI | Kanban, agent list, task sessions, logs, diffs, and artifacts. |

This topology is intentionally close to distributed mode. The only local
assumptions are process placement and filesystem access.

## FleetDB Local Mode Contract

FleetDB is the source of truth for local mode. A local startup command must
bring up Redis and FleetDB before Loom creates workspaces, daemon profile,
agent definitions, sessions, tasks, leases, commands, or artifacts.

The local stack should use normal FleetDB URLs and resource paths, for
example:

| Resource | Expected path shape |
|---|---|
| Health | `/healthz` |
| Workspace repos | `/api/v1/{workspace}/repos` |
| Daemon profile | `/api/v1/{workspace}/daemon` |
| Roles | `/api/v1/{workspace}/roles` |
| Agent definitions | `/api/v1/{workspace}/agents` |
| Nodes and heartbeats | `/api/v1/{workspace}/nodes` |
| Agent sessions | `/api/v1/{workspace}/agent-sessions` |
| Terminal sessions | `/api/v1/{workspace}/terminal-sessions` |
| Leases | `/api/v1/{workspace}/agent-leases` |
| Commands | `/api/v1/{workspace}/agent-commands` |
| Artifacts | `/api/v1/{workspace}/artifacts` |
| Issues | FleetDB issue endpoints through Loom's FleetDB backend. |

Local mode may use a deterministic backend for dogfood testing, but the
backend is only a model substitute. It must not replace FleetDB as the
coordination store.

## FleetDB Session Contract

The first local-mode slice records agent execution in FleetDB
`agent-sessions`. The Loom daemon creates a session before launching the
backend process, updates it when the process is running, and finalizes it on
exit.

Every local agent session row must include:

- `workspace_key`
- `session_id`
- `agent_id`
- `node_id`
- `kind`
- `phase`
- `status`
- `task_id` after task claim
- `started_at`
- `last_heartbeat` while running
- `finished_at` for terminal states
- `exit_code` for exited processes
- `error_class` for failed exits

Session `metadata` must include the first MVP artifact pointers and summary
fields:

- `backend`
- `task_id`
- `transcript_path`
- `log_path`
- `files_changed`
- `lines_added`
- `lines_removed`
- `files_touched`

The task Sessions tab should load sessions by querying FleetDB
`/api/v1/{workspace}/agent-sessions?task_id={task}` through Loom's shared
store interface. Server-visible local files may be used to open transcripts
or diffs only when FleetDB already points to those artifacts. The UI should
not infer task/session ownership by scanning local directories when a
FleetDB session record exists.

FleetDB write payloads are part of the product contract. PATCH requests must
use the public snake_case fields (`status`, `task_id`, `finished_at`,
`exit_code`, etc.) and omit absent fields. Go struct field names or null
patches for unset fields are compatibility bugs because they create records
that local and distributed readers interpret differently.

## One-Command Dogfood Harness

The dogfood harness should be a single command that:

1. Starts Redis, FleetDB, Loom serve, Loom daemon, and the Web UI.
2. Creates a `LOCALMODE` workspace and a fixture source repo.
3. Registers one planner agent and one coder agent through FleetDB.
4. Seeds one planning task and one approved coding task through the normal
   Loom issue API backed by FleetDB.
5. Runs the planner and coder through the same daemon/session path used by
   distributed mode.
6. Leaves visible data in the Kanban board, agent sidebar, task Sessions tab,
   logs, diffs, and FleetDB session/artifact APIs.

This harness is a product acceptance test, not a separate architecture. Any
local-mode-only behavior must either be removed or explicitly promoted into
the shared distributed contract.

Current dogfood proof for this contract:

- `LOCALMODE-1` is planned and moved to review.
- `LOCALMODE-2` is implemented and closed.
- FleetDB stores completed `local-planner` and `local-coder` sessions with
  `task_id`.
- The task Sessions tab shows the completed `local-coder` run, transcript
  availability, and diff summary.

## Supported Launch Paths

| Launch path | Expected behavior |
|---|---|
| UI starts local planner/coder | Server creates run, starts process, streams status. |
| Daemon/supervisor starts configured agent | Run/session visible through monitor and task views. |
| User runs `loom plan` or `loom task` directly | CLI publishes run/session to active server when available. |

## Required Behavior

### Agent Identity

- Local agents must have a stable name.
- Direct CLI runs may accept `--agent`, use `LOOM_AGENT_NAME`, or generate
  an ad hoc name.
- Task claims must use that name.

### Run Registration

- Create a run record before invoking the backend CLI.
- Create a session record before task claim.
- Update the run after task claim with task ID and task title.
- Finalize the run on process exit.

### Shared Runtime

- Local runs must write session data to a server-visible workspace runtime.
- The UI should not depend on scanning an individual worktree's private
  session directory.
- If the CLI cannot find a server-visible runtime, it must warn the user.

### Process State

The UI should show:

- local PID
- command
- working directory
- backend
- role
- current task
- started at
- last heartbeat
- exit code

### Preflight

Before launch, local mode should check:

- backend CLI installed
- backend credentials usable
- workspace exists
- repo path exists
- agent worktree exists or can be created
- required tools exist
- gate command exists
- git remote exists when push is required

## User Flows

### Start Planner From UI

1. User clicks "Start planner".
2. UI asks for role/backend if not already configured.
3. Server runs preflight.
4. Server creates run/session.
5. Process starts.
6. Agent claims a task needing design.
7. Task card shows claimed agent.
8. Sessions tab shows running session.
9. Design is written.
10. Run completes and session is finalized.

### Run Direct CLI

1. User runs `loom task --agent codex-coder`.
2. CLI discovers active local server or uses `LOOM_SERVER_URL`.
3. CLI creates run/session through the server.
4. CLI starts backend.
5. UI shows the same run as if it were UI-launched.

## MVP Requirements

- Direct and daemon-launched runs share one session list.
- Agent sidebar shows idle configured agents and active local runs.
- Task Sessions tab populates during local agent execution.
- Run completion records exit code, error class, and artifacts.
- Preflight failure is visible in task detail if a task was selected.
- FleetDB exposes every control-plane resource the local UI/daemon path
  needs; missing FleetDB endpoints are release blockers, not local-mode
  bypass candidates.

## Failure Handling

| Failure | Expected UX |
|---|---|
| Backend auth missing | Preflight failed with setup action. |
| Worktree missing | Offer repair/create worktree. |
| Gate command missing | Mark run warning or failure with configured action. |
| Push remote missing | Complete implementation but show push warning. |
| Server unavailable | CLI warns run will not be UI-visible. |
| Process crashes | Session finalized as failed with exit code. |

## Acceptance Criteria

- Local planner started from UI appears in agent sidebar within 2 seconds.
- Direct `loom task` run appears in task Sessions tab.
- Killing the process finalizes the run as failed or aborted.
- Completed sessions survive server restart.
- The UI shows actionable messages for missing auth, tools, gate command,
  worktree, or remote.
- The same FleetDB HTTP endpoints are used when the stack runs on one
  machine and when services are split across multiple containers.

## Open Questions

- Should direct CLI auto-start the local server when unavailable?
- Should local mode require a configured agent definition or allow ad hoc
  runs by default?
- Should missing `make gate` block completion or be a warning when the
  repo has no configured gate?
