# Local Mode Podman E2E Runbook

**Status:** Draft
**Date:** 2026-05-04
**Related:** `docs/product/local-mode-product-spec.md`,
`docs/product/dogfood-agent-execution-test-plan.md`,
`test/local-mode/README.md`

This runbook documents the local-mode dogfood stack used to test supervised
planner and coder agents end to end inside Podman containers.

The stack is local mode, not a separate product architecture. It runs every
process on one machine, but Loom still uses FleetDB as the shared control
plane for workspaces, issues, agent definitions, leases, sessions, terminal
sessions, and artifacts.

## Quick Start

From the `loomcli` repo root:

```sh
make local-mode-codex-up
```

Open the UI:

```text
http://localhost:8283/ws/LOCALMODE/kanban
```

Useful endpoints:

```text
UI:       http://localhost:8283/ws/LOCALMODE/kanban
Loom API: http://localhost:8282
FleetDB:  http://localhost:8280
Config:   http://localhost:8282/api/config
Agents:   http://localhost:8282/api/monitor/agents?workspace=LOCALMODE
Task 1 sessions: http://localhost:8282/api/workspaces/LOCALMODE/tasks/LOCALMODE-1/sessions
Task 2 sessions: http://localhost:8282/api/workspaces/LOCALMODE/tasks/LOCALMODE-2/sessions
```

The deterministic `make local-mode-up` path uses the same services, but its
seeded task IDs are `LM-PLAN-1` and `LM-CODE-1`.

Stop and remove the stack volumes:

```sh
make local-mode-down
```

Follow logs:

```sh
make local-mode-logs
```

## Prerequisites

- Podman with Compose support, or `podman-compose`.
- A built Web UI, or enough host tooling for `make local-mode-codex-up` to
  build `internal/webui/frontend/dist` once.
- Local Codex credentials at `${HOME}/.codex`, or set
  `LOCAL_MODE_CODEX_HOME=/path/to/.codex`.
- Enough Podman VM disk for the Go image, FleetDB image, npm/Codex install,
  workspace volumes, and build cache. On macOS, allocate at least 25 GB for
  the Podman machine before rebuilding this stack repeatedly.

If disk is already exhausted, inspect usage first:

```sh
podman system df
```

Then remove only data you are willing to lose. `make local-mode-down` removes
this stack's volumes. `podman system prune` removes broader unused Podman
data.

## Containers

The Compose project is `loomcli-local-mode`.

| Service | Responsibility | Host port |
|---|---|---|
| `redis` | FleetDB backing store for the dogfood stack. | none |
| `fleet-db` | Shared issue store and control-plane API. Loom talks to this through the same FleetDB client used by distributed mode. | `8280` |
| `loom-local` | Builds and runs `loom`, creates the `LOCALMODE` workspace, creates fixture repos and worktrees, registers planner/coder agent definitions, seeds tasks, starts `loom serve`, then runs `loom daemon`. | `8282` |
| `ui-local` | Caddy serving `internal/webui/frontend/dist` and proxying API/WebSocket traffic to `loom-local`. | `8283` |

The deterministic backend is not its own container. In `make local-mode-up`,
`loom-local` uses the `loom-backend-localdogfood` command as the model
substitute so the same daemon/session flow can run without external auth.

The Codex run uses the same `loom-local` container and same Loom daemon path,
but the override file installs Codex CLI and sets:

```text
LOOM_BACKEND=codex
LOOM_LOCAL_MODE_PLAN_AGENT=codex-planner
LOOM_LOCAL_MODE_CODE_AGENT=codex-coder
LOOM_LOCAL_MODE_PLAN_TASK_ID=LOCALMODE-1
LOOM_LOCAL_MODE_CODE_TASK_ID=LOCALMODE-2
```

## Commands

Run the deterministic local dogfood path:

```sh
make local-mode-up
```

Run the real Codex local dogfood path:

```sh
make local-mode-codex-up
```

Use a different Codex home:

```sh
LOCAL_MODE_CODEX_HOME=/path/to/.codex make local-mode-codex-up
```

Use a different Codex CLI version:

```sh
LOCAL_MODE_CODEX_CLI_VERSION=0.128.0 make local-mode-codex-up
```

Override ports when defaults are busy:

```sh
LOCAL_MODE_FLEETDB_PORT=8380 \
LOCAL_MODE_API_PORT=8382 \
LOCAL_MODE_UI_PORT=8383 \
make local-mode-codex-up
```

Verify the container has bash:

```sh
podman exec loomcli-local-mode-loom-local-1 bash -lc 'echo "$SHELL"; command -v bash'
```

Some Compose implementations use underscores in generated container names. If
the command above cannot find the container, run `podman ps` and substitute
the actual `loom-local` container name.

## Expected E2E Flow

`make local-mode-codex-up` should create:

- Workspace: `LOCALMODE`
- Planner agent: `codex-planner`
- Coder agent: `codex-coder`
- Planning task: `LOCALMODE-1`
- Approved coding task: `LOCALMODE-2`
- Source repo: `/workspace/source-repo`
- Workspace repo: `/root/.loom/workspaces/LOCALMODE/source-repo`
- Agent worktrees under
  `/root/.loom/workspaces/LOCALMODE/worktrees/source-repo/`

The expected run is:

1. `codex-planner` starts through `loom daemon`.
2. It claims `LOCALMODE-1`.
3. It runs Codex CLI through the supervised backend path.
4. Loom records an agent session in FleetDB with `task_id=LOCALMODE-1`.
5. The planner writes a design and moves the task to review.
6. `codex-coder` starts through the same daemon path.
7. It claims `LOCALMODE-2`, which already has an approved design.
8. It writes `local-mode-agent-output.txt`, commits in its worktree, and
   closes the task.
9. Loom finalizes the FleetDB agent session with transcript, log, diff, and
   status metadata.

## UI Evidence

Use the UI to validate the product behavior, not just process exit codes.

In the Kanban board:

- `LOCALMODE-1` should leave planning evidence and move to review.
- `LOCALMODE-2` should close after the coder completes.

In the left agent panel:

- Active agents show while the daemon process is running them.
- Completed agents may show `Idle`. That is availability, not run history.
- Historical evidence belongs in the task Sessions tab.

In a task Sessions tab:

- A session row should exist for the agent that claimed the task.
- Status should be terminal after the process exits.
- Logs should open from the session metadata.
- Transcript presence should be visible.
- Diff stats should appear for coding runs.
- Files should show the committed/touched files when artifacts are available.

## API Evidence

Check the Loom API:

```sh
curl -sS http://localhost:8282/api/config
curl -sS 'http://localhost:8282/api/monitor/agents?workspace=LOCALMODE'
curl -sS http://localhost:8282/api/workspaces/LOCALMODE/tasks/LOCALMODE-1/sessions
curl -sS http://localhost:8282/api/workspaces/LOCALMODE/tasks/LOCALMODE-2/sessions
```

Check the container logs:

```sh
make local-mode-logs
```

Check the worktree inside the container:

```sh
podman exec loomcli-local-mode-loom-local-1 bash -lc \
  'cd /root/.loom/workspaces/LOCALMODE/worktrees/source-repo/codex-coder && git log --oneline -5 && git status --short'
```

## Troubleshooting

### UI Shows Agents As Idle

The left panel shows current agent availability from the monitor API. After
the planner or coder exits, `Idle` can be correct. Open the task Sessions tab
to inspect the completed run, transcript, logs, diff, and files.

### Sessions Tab Is Empty

This means the daemon did not publish a FleetDB `agent-sessions` record with
the claimed task ID, or the UI/API could not query it. Check:

```sh
curl -sS http://localhost:8282/api/workspaces/LOCALMODE/tasks/LOCALMODE-2/sessions
make local-mode-logs
```

Look for session creation, task claim, lease creation, backend start, and
session finalization messages.

### Logs, Diff, Or Files Show 404

The session row exists, but the artifact path or route cannot serve the
artifact. Check whether the session metadata contains `log_path`,
`transcript_path`, `files_changed`, and diff summary fields. The UI should
not invent local paths; it should follow FleetDB-backed session/artifact
metadata.

### Terminal Shows Disconnected

The local-mode dogfood stack validates agent execution first. A disconnected
terminal usually means no terminal session was created for the requested
workspace/path, or the WebSocket route cannot attach to one. Verify the Loom
API is reachable and check `make local-mode-logs` for terminal session or
WebSocket errors.

### Codex Auth Fails

The Codex override mounts `${LOCAL_MODE_CODEX_HOME:-${HOME}/.codex}` at
`/codex-host:ro`, then copies `auth.json` and `config.toml` into
`/root/.codex` inside the container. If `/root/.codex/auth.json` is missing
or empty, startup fails before agent registration.

Use:

```sh
LOCAL_MODE_CODEX_HOME=/path/to/.codex make local-mode-codex-up
```

### Port Conflicts

Defaults are:

```text
FleetDB: 8280
Loom API: 8282
Web UI: 8283
```

Override all three ports when running multiple stacks.

### Podman VM Is Full

The Codex image installs Node/npm and Codex CLI in addition to the Go build
image. On macOS, increase the Podman machine disk to at least 25 GB or prune
unused images, containers, caches, and volumes after confirming they are not
needed.

### Podman Machine Boots But Compose Cannot Connect

On macOS Apple Silicon, a broken Podman machine can report start success
while the VM is not actually usable. Confirm with:

```sh
podman machine list
podman info
```

If `podman info` cannot connect, fix or recreate the Podman machine before
debugging Loom.

## Parity Notes

This runbook should remain aligned with distributed mode:

- The same FleetDB resource path is used for control-plane state.
- The same Loom daemon/session path starts deterministic and Codex agents.
- Containers provide packaging and isolation only.
- Any missing endpoint or artifact should be fixed in the shared FleetDB or
  Loom API path, not by adding a local-only UI fallback.
