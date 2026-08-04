# Local Mode Product Spec

> **Status:** Largely implemented — the control-plane topology invariant below
> is CI-enforced (`scripts/check-control-plane-paths.sh` via
> `make check-control-plane-paths`), and the one-command dogfood harness shipped
> as `make local-mode-up`. Preflight and some UI/failure-handling requirements
> are still partial; they are marked inline. *audited 2026-08-03*

**Date:** 2026-05-04
**Related:** see [Related](#related) at the bottom.

## Purpose

Local mode is the first shippable slice of visible agent execution. It
assumes one machine, one shared filesystem, one Loom server, and local
agent processes.

Local mode must still use fleet-db as the shared control plane. It is a
single-machine deployment of the distributed architecture, not a separate
storage mode.

The only valid runtime control-plane topologies are:

```text
local mode:
loomcli -> HTTP client -> fleet-db subprocess -> RedisStorage -> miniredis or external Redis

cloud mode:
loomcli -> HTTP client -> fleet-db service -> Redis/Postgres
```

`internal/infra/memstore` is a test-only store double. It is not a local-mode
runtime, fallback, cache, or embedded Redis implementation.

**This is enforced, not aspirational.** `scripts/check-control-plane-paths.sh`
fails the gate when runtime (non-test) code imports `internal/infra/memstore`,
when anything outside `internal/bootstrap/openstore.go` imports or constructs
the fleet-db store client, or when `internal/bootstrap/mode.go` grows a third
`Mode*` constant. The gate reaches it through `check-go`
(`Makefile:502`, which invokes the script at `:512`), run by `check:` at
`:554` and `gate: check` at `:578`; it is also wired into `lint`
(`:275-278`) and runnable standalone as `check-control-plane-paths`
(`:293-294`).

### Naming trap: two different "local modes"

- **Local mode (the product mode)** is the deployment shape `bootstrap.DetectMode`
  returns when `LOOM_FLEET_DB_URL` is unset: `loom serve` spawns an embedded
  fleet-db subprocess and state lives under `bootstrap.LoomDir()`
  (`internal/bootstrap/mode.go:26-58`, `internal/bootstrap/paths.go:39-51`).
  It involves no containers.
- **The local-mode dogfood stack** is the compose stack under `test/local-mode/`
  driven by `make local-mode-up`. It *does* use containers — that is a test
  harness choice, not part of the product mode. See
  [One-Command Dogfood Harness](#one-command-dogfood-harness).

Say which one you mean.

## Product Promise

When a user starts a local agent, the UI shows it immediately, attaches it
to the task it claims, records the session, and preserves enough evidence
to debug the result.

## fleet-db Parity Requirement

Local mode and distributed mode must use the same persistence and API path
for agent-visible state:

- workspaces and repos
- roles and agent definitions
- daemon profile
- control-plane nodes and heartbeats
- agent sessions
- terminal sessions
- agent leases and agent ownership leases
- agent commands
- artifacts
- issues, comments, dependencies, labels, and mutation streams

The local stack may run every process on one machine, but Loom must talk to
fleet-db for these resources through the same `internal/infra/fleetdb`
client used by distributed mode. Both modes reach that client through the
single opener `internal/bootstrap/openstore.go`; `DetectMode` picks the
branch and nothing else in the tree makes the local/cloud distinction
(`internal/bootstrap/mode.go:51-58`).

Local mode must not introduce:

- a Loom-only `memstore` control-plane path
- a local JSON/file database for agent state
- a sidecar API that implements different behavior than fleet-db
- UI routes that read local runtime files instead of fleet-db-backed
  session/artifact records when fleet-db records exist

If fleet-db lacks an endpoint needed by local mode, the product fix is to
add that endpoint to fleet-db and keep the local dogfood test as a parity
test for distributed mode. (fleet-db lives in a **separate repository** — see
`docs/loom-glossary.md`.)

## Local Topology

The first shippable local mode should start this topology with one command:

| Process | Responsibility |
|---|---|
| Redis (or in-process miniredis) | Durable backing store for fleet-db. |
| fleet-db | Issue store and Loom control-plane API. |
| `loom serve` | Web/API server for the workspace UI. |
| `loom daemon` | Local supervisor that starts worker agents. |
| Agent backend CLI | Deterministic dogfood backend, or a real agent backend (`codex`, `claude`, …). |
| Web UI | Kanban, agent list, task sessions, logs, diffs, and artifacts. |

In the embedded case `loom serve` starts the fleet-db subprocess and the
miniredis it talks to itself (`bootstrap.StartEmbedded`,
`internal/bootstrap/embedded.go:300-305`); the miniredis snapshot persists at
`<dataDir>/fleet-db/redis-snapshot.json`. A healthy embedded runtime is reused
rather than respawned per invocation
(`internal/bootstrap/embedded.go:183,221`).

This topology is intentionally close to distributed mode. The only local
assumptions are process placement and filesystem access.

## Desktop App Relationship

The installable macOS app is a packaging and lifecycle layer for this same
local-mode topology. It must not introduce a second persistence model.

In the desktop shape:

- Tauri owns windows, menu state, preferences, and service controls.
- A per-user macOS LaunchAgent (`com.loom.local`) runs `loom local service` so
  background agents survive app quit, logout/login, and reboot after the user
  logs in again (`internal/cli/local/launchagent.go:15,178-184`).
- The bundled `loom` runtime starts embedded fleet-db/miniredis, the Loom
  web/API server, and the workspace daemon manager
  (`internal/cli/local/local_cmd.go:241-249`, `internal/cli/local/daemon.go:29-33`).
- fleet-db remains the source of truth for workspaces, issues, agents,
  sessions, leases, commands, and artifacts.
- Desktop app data lives under `~/Library/Application Support/Loom/data`, with
  `LOOM_DESKTOP_DATA_DIR` / `LOOM_CONFIG_DIR` set explicitly for the
  app-managed runtime (`internal/cli/local/runtime.go:106-128`).

See [`desktop-app-runtime-spec.md`](desktop-app-runtime-spec.md) for the desktop
runtime, LaunchAgent, CLI coexistence, update, and multi-window contract.

## fleet-db Local Mode Contract

fleet-db is the source of truth for local mode. A local startup command must
bring up Redis and fleet-db before Loom creates workspaces, daemon profile,
agent definitions, sessions, tasks, leases, commands, or artifacts. Embedded
startup blocks until fleet-db's `/healthz` reports ready
(`internal/bootstrap/embedded.go:266-301`).

The local stack uses normal fleet-db URLs and resource paths. Every row below
is the path the shared client actually issues:

| Resource | Path shape | Client |
|---|---|---|
| Health | `/healthz` | `netutil.WaitForHealthz`, `internal/bootstrap/embedded.go:205` |
| Workspace repos | `/api/v1/{workspace}/repos` | `internal/infra/fleetdb/repo.go:73-115` |
| Daemon profile | `/api/v1/{workspace}/daemon` | `internal/infra/fleetdb/daemon.go:149,158` |
| Roles | `/api/v1/{workspace}/roles` | `internal/infra/fleetdb/role.go:103,121` |
| Agent definitions | `/api/v1/{workspace}/agents` | `internal/infra/fleetdb/agent.go:110,128` |
| Control-plane nodes | `/api/v1/{workspace}/nodes`, `…/nodes/{id}/heartbeat` | `internal/infra/fleetdb/control_plane.go:42-83` |
| Agent sessions | `/api/v1/{workspace}/agent-sessions` | `internal/infra/fleetdb/control_plane.go:120-208` |
| Terminal sessions | `/api/v1/{workspace}/terminal-sessions` | `internal/infra/fleetdb/control_plane.go:259-308` |
| Agent (session) leases | created at `/api/v1/{workspace}/agent-sessions/{session}/leases`; read/heartbeat/release at `/api/v1/{workspace}/agent-leases/{lease}` | `internal/infra/fleetdb/control_plane.go:601-659` |
| Agent ownership leases | `/api/v1/{workspace}/agent-ownership-leases/{agent}/{acquire,heartbeat,release}` | `internal/infra/fleetdb/control_plane.go:678-736` |
| Commands | `/api/v1/{workspace}/agent-commands` | `internal/infra/fleetdb/control_plane.go:749-806` |
| Artifacts | `/api/v1/{workspace}/artifacts` | `internal/infra/fleetdb/control_plane.go:341-419` |
| Issues | `/api/v1/{workspace}/issues…` — a **separate client** from the rows above: the *issue* backend (`internal/backend`), not the control-plane store (`internal/infra/fleetdb`) | `internal/backend/fleet/blocked.go:12`, `internal/backend/fleet/claim_release.go:27` |

Note the lease creation asymmetry: an agent (session) lease is created under its
agent session, not under a flat `/agent-leases` collection.

Local mode may use a deterministic agent backend for dogfood testing, but that
backend is only a model substitute. It must not replace fleet-db as the
coordination store.

## fleet-db Session Contract

The first local-mode slice records agent execution in fleet-db
`agent-sessions`. The Loom daemon creates a session before launching the
agent backend process, updates it when the process is running, and finalizes it
on exit.

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

The field set is `domain.AgentSession` (`internal/domain/control_plane.go:81-102`).

Session `metadata` must include the first MVP artifact pointers and summary
fields:

- `backend` (the **agent** backend — see `docs/loom-glossary.md`)
- `task_id`
- `transcript_path`
- `log_path`
- `files_changed`
- `lines_added`
- `lines_removed`
- `files_touched`

The task Sessions tab should load sessions by querying fleet-db
`/api/v1/{workspace}/agent-sessions?task_id={task}` through Loom's shared
store interface. Server-visible local files may be used to open transcripts
or diffs only when fleet-db already points to those artifacts. The UI should
not infer task/session ownership by scanning local directories when a
fleet-db session record exists.

fleet-db write payloads are part of the product contract. PATCH requests must
use the public snake_case fields (`status`, `task_id`, `finished_at`,
`exit_code`, etc.) and omit absent fields. Go struct field names or null
patches for unset fields are compatibility bugs because they create records
that local and distributed readers interpret differently. The client builds
those bodies through explicit helpers rather than marshalling domain structs
(`internal/infra/fleetdb/body_helpers.go`).

## One-Command Dogfood Harness

**Shipped.** The harness is a compose stack under `test/local-mode/`, driven by
make targets (`Makefile:162-224`):

| Target | What it starts |
|---|---|
| `make local-mode-up` | Deterministic dogfood agent backend (`test/local-mode/loom-backend-localdogfood`) |
| `make local-mode-codex-up` | Same stack with the real `codex` agent backend |
| `make local-mode-claude-up` | Same stack with the real `claude` agent backend |
| `make local-mode-daytona-up` | Claimed task runs inside a real Daytona sandbox; needs `DAYTONA_API_KEY` |
| `make local-mode-down` | Tear down, including volumes |
| `make local-mode-logs` | Follow `loom-local` and `ui-local` |
| `make local-mode-verify` | `test/local-mode/verify-local-mode.sh` |
| `make local-mode-routing-verify` | `test/local-mode/verify-agent-routing.py` — proves role-based routing for UI-registered agents |
| `make local-mode-webhook-verify` | `test/local-mode/verify-webhook.sh` |

The stack serves the workspace `LOCALMODE` at
`http://localhost:${LOCAL_MODE_UI_PORT:-8283}/ws/LOCALMODE/kanban`.

What the harness must do:

1. Start Redis, fleet-db, `loom serve`, `loom daemon`, and the Web UI.
2. Create a `LOCALMODE` workspace and a fixture source repo.
3. Register agents through fleet-db.
4. Seed tasks through the normal Loom issue API backed by fleet-db.
5. Run those agents through the same daemon/session path used by distributed
   mode.
6. Leave visible data in the Kanban board, agent sidebar, task Sessions tab,
   logs, diffs, and fleet-db session/artifact APIs.

This harness is a product acceptance test, not a separate architecture. Any
local-mode-only behavior must either be removed or explicitly promoted into
the shared distributed contract.

*History: earlier revisions of this spec recorded a point-in-time dogfood
result (issues `LOCALMODE-1`/`LOCALMODE-2`, specific `local-planner` /
`local-coder` session rows). That evidence is not reproducible from this
repository and has been cut; `make local-mode-verify` is the live equivalent.*

## Supported Launch Paths

| Launch path | Expected behavior |
|---|---|
| UI starts an agent | Server creates run, starts process, streams status. |
| `loom daemon` starts a configured agent | Run/session visible through monitor and task views. |
| User runs `loom plan` or `loom task` directly (`internal/cli/agent/plan.go:36`, `internal/cli/agent/task.go:28`) | CLI publishes run/session to the active server when available. |

## Required Behavior

### Agent Identity

- Local agents must have a stable name.
- Direct CLI runs may accept `--agent`, use `LOOM_AGENT_NAME`
  (`bootstrap.EnvAgentName`), or generate an ad hoc name.
- Task claims must use that name.

### Run Registration

- Create a run record before invoking the agent backend CLI.
- Create a session record before task claim.
- Update the run after task claim with task ID and task title.
- Finalize the run on process exit.

### Shared Runtime

- Local runs must write session data to a server-visible workspace runtime.
- The UI should not depend on scanning an individual worktree's private
  session directory.
- If the CLI cannot find a server-visible runtime, it must warn the user.

### Process State

The UI should show: local PID, command, working directory, agent backend, role,
current task, started at, last heartbeat, exit code.

### Preflight

**Partially implemented.** Two distinct preflight paths exist today and neither
covers the full list this spec asked for:

- `internal/runtimepreflight` is the fail-closed check that runs *before a
  local task runner is queued*: it resolves the effective agent backend and runs
  that backend's `HealthCheck`, so `loom epic run` and the UI epic-start path
  fail with an actionable message instead of fake-completing
  (`internal/runtimepreflight/preflight.go`; entry point
  `PreflightLocalTaskRunner`).
- The daemon supervisor's `preFlightSetup` gates on agent-backend availability
  (`ErrBackendUnavailable`, "backend binary not on PATH") and then does recovery
  detection, yield-file cleanup, epic assignment and task claim
  (`internal/cli/daemon/supervisor/supervisor.go:426-456`).

The remaining checks below are the target, **not** verified as implemented:
backend credentials usable, workspace exists, repo path exists, agent worktree
exists or can be created, required tools exist, gate command exists, git remote
exists when push is required. `loom doctor` covers several of these
out-of-band as named checks — `backend_cli`, `git_repo`, `worktrees`,
`project_config`, `global_config`, `fleetdb`, `issue_backend`
(`internal/cli/doctor/doctor_checks.go`, `doctor_fleetdb.go`) — but it is a
separate operator command, not a launch gate.

## User Flows

### Start An Agent From The UI

1. User starts an agent.
2. UI asks for role/agent backend if not already configured.
3. Server runs preflight.
4. Server creates run/session.
5. Process starts.
6. Agent claims an eligible task.
7. Task card shows the claiming agent.
8. Sessions tab shows the running session.
9. Work is written.
10. Run completes and session is finalized.

### Run Direct CLI

1. User runs `loom task --agent codex-coder`.
2. CLI discovers the active local server or uses `LOOM_SERVER_URL`.
3. CLI creates run/session through the server.
4. CLI starts the agent backend.
5. UI shows the same run as if it were UI-launched.

## MVP Requirements

- Direct and daemon-launched runs share one session list.
- Agent sidebar shows idle configured agents and active local runs.
- Task Sessions tab populates during local agent execution.
- Run completion records exit code, error class, and artifacts.
- Preflight failure is visible in task detail if a task was selected.
- fleet-db exposes every control-plane resource the local UI/daemon path
  needs; missing fleet-db endpoints are release blockers, not local-mode
  bypass candidates.

## Failure Handling

| Failure | Expected UX |
|---|---|
| Agent backend auth missing | Preflight failed with setup action. |
| Worktree missing | Offer repair/create worktree. |
| Gate command missing | Mark run warning or failure with configured action. |
| Push remote missing | Complete implementation but show push warning. |
| Server unavailable | CLI warns run will not be UI-visible. |
| Process crashes | Session finalized as failed with exit code. |

Error classes are enumerated in
[`error-class-reference.md`](error-class-reference.md); recovery UX in
[`failure-modes-recovery-ux.md`](failure-modes-recovery-ux.md).

## Acceptance Criteria

- A locally started agent appears in the agent sidebar within 2 seconds.
- Direct `loom task` run appears in task Sessions tab.
- Killing the process finalizes the run as failed or aborted.
- Completed sessions survive server restart.
- The UI shows actionable messages for missing auth, tools, gate command,
  worktree, or remote.
- The same fleet-db HTTP endpoints are used when the stack runs on one
  machine and when services are split across multiple containers.

## Open Questions

- Should direct CLI auto-start the local server when unavailable?
- Should local mode require a configured agent definition or allow ad hoc
  runs by default?
- Should missing `make gate` block completion or be a warning when the
  repo has no configured gate?

## Related

- [`daemon-agent-runtime-architecture.md`](daemon-agent-runtime-architecture.md)
  — agent placement vs task assignment, and the agent ownership lease that makes
  multiple daemons safe.
- [`desktop-app-runtime-spec.md`](desktop-app-runtime-spec.md) — the macOS
  packaging of this same topology.
- [`desktop-installation-runbook.md`](desktop-installation-runbook.md) — how to
  build, install and verify that packaging.
- [`container-runner-mvp-spec.md`](container-runner-mvp-spec.md) — the unbuilt
  container *agent* runner proposal, and what shipped instead.
- [`agent-execution-prd.md`](agent-execution-prd.md) — the parent PRD.
- [`agent-run-ux-spec.md`](agent-run-ux-spec.md) and
  [`session-artifact-contract.md`](session-artifact-contract.md) — what the UI
  shows and what a session must persist.
- [`dogfood-agent-execution-test-plan.md`](dogfood-agent-execution-test-plan.md)
  — the test plan this harness serves.
- `docs/testing/local-mode-podman-e2e.md` — the podman variant of the stack.
- `docs/design/distributed-control-plane.md` and
  `docs/design/2026-07-23-control-plane-as-built.md` — the multi-node
  architecture this is a single-machine deployment of.
- `docs/loom-glossary.md` — "local mode", "local-only workspace", "fleet mode",
  and the three senses of "backend".
