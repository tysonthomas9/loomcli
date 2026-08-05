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
```

Planner and coder task IDs are minted by FleetDB for each run. The container's
`/tmp/loom-local-mode-run.json` records the exact IDs, run start time, backend,
checkout, and Compose project. `make local-mode-verify` reads it automatically.

Stop and remove the stack volumes:

```sh
make local-mode-down
```

Follow logs:

```sh
make local-mode-logs
```

Verify the Codex seeded daemon/agent/session flow from the host:

```sh
make local-mode-codex-verify
```

For the deterministic `make local-mode-up` stack, verify the deterministic
task IDs:

```sh
make local-mode-verify
```

The verifier polls the running stack until the manifest-owned planner task is
in review with a design and the manifest-owned coder task is in review with a
`local-branch:loom/<task>@<sha>` artifact reference. Both tasks must have
completed sessions and transcript entries created after this run began, and
the coder session must expose a diff containing `local-mode-agent-output.txt`.
The container captures the threshold immediately
before seeding on the same VM clock FleetDB uses; malformed or historical
timestamps fail closed. Historical volume data cannot satisfy a new run.

The local-mode FleetDB port is host-loopback-only. FleetDB runs with API-key
authentication and RBAC enabled, and the profile bootstraps the deterministic
`loom-local-mode-test-only-admin-key-v1` admin fixture so Compose and host-side
proof scripts use the same credential. This is a checked-in test key for a
disposable local stack, not a secret suitable for shared or production use.
Use `LOCAL_MODE_FLEETDB_API_KEY=<test-key>` to override it consistently across
FleetDB, Loom, and the verification scripts.
Set `LOCAL_MODE_FLEETDB_SOURCE_ROOT=/absolute/path/to/fleet-db-worktree` when
the proof must build a paired feature branch instead of the default sibling
`fleet-db` checkout.

## Prerequisites

- Podman with Compose support, or `podman-compose`.
- A built Web UI, or enough host tooling for `make local-mode-codex-up` to
  build `internal/webui/frontend/dist` once.
- Local Codex credentials at `${HOME}/.codex`, or set
  `LOCAL_MODE_CODEX_HOME=<codex-home>`.
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

The default Compose project is `loomcli-local-mode-<checkout-id>`, where the
checkout ID is a stable hash of the physical repo path. Run
`make local-mode-info` to print the resolved values. A provenance marker in the
`loom-data` volume must match both the source root and project; startup refuses
unmarked legacy data and cross-checkout reuse.

Teardown remains project-name based because Compose cannot portably inspect
provenance in every stopped or unmarked legacy volume before `down -v`. An
explicit `LOCAL_MODE_COMPOSE_PROJECT` selects the volumes to destroy. Confirm
the exact project with `make local-mode-info` and repeat it on verify, logs,
and teardown.

| Service      | Responsibility                                                                                                                                                                                        | Host port |
| ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------- |
| `redis`      | FleetDB backing store for the dogfood stack.                                                                                                                                                          | none      |
| `fleet-db`   | Shared issue store and control-plane API. Loom talks to this through the same FleetDB client used by distributed mode.                                                                                | `8280`    |
| `loom-local` | Builds and runs `loom`, creates the `LOCALMODE` workspace and fixture repository, creates canonical planner/coder Agents, seeds tasks, and hosts their trigger/workflow execution in `loom serve`. | `8282`    |
| `ui-local`   | Caddy serving `internal/webui/frontend/dist` and proxying API/WebSocket traffic to `loom-local`.                                                                                                      | `8283`    |

The deterministic backend is not its own container. In `make local-mode-up`,
`loom-local` uses the `loom-backend-localdogfood` command as the model
substitute so the same daemon/session flow can run without external auth.

The Codex run uses the same `loom-local` container and same Loom daemon path,
but the override file installs Codex CLI and sets:

```text
LOOM_BACKEND=codex
LOOM_LOCAL_MODE_PLAN_AGENT=codex-planner
LOOM_LOCAL_MODE_CODE_AGENT=codex-coder
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

Force a specific Compose runner when auto-detection picks the wrong one:

```sh
LOCAL_MODE_COMPOSE="docker compose" make local-mode-codex-up
```

Use a different Codex home:

```sh
LOCAL_MODE_CODEX_HOME=<codex-home> make local-mode-codex-up
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

Different checkouts already receive distinct default projects and volumes,
but they still compete for the default host ports. Override all three ports
when running stacks concurrently.

Run a second stack in parallel by changing both the Compose project and ports:

```sh
LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-b \
LOCAL_MODE_FLEETDB_PORT=8380 \
LOCAL_MODE_API_PORT=8382 \
LOCAL_MODE_UI_PORT=8383 \
LOCAL_MODE_COMPOSE_UP_FLAGS="--build -d" \
make local-mode-codex-up
```

Verify the second stack through its API port:

```sh
LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-b \
LOCAL_MODE_API_PORT=8382 \
make local-mode-codex-verify
```

The Codex verifier also rejects a live manifest whose backend is not `codex`.

Use the same project name for logs and teardown:

```sh
LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-b make local-mode-logs
LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-b make local-mode-down
```

Append one or more Compose override files with `LOCAL_MODE_COMPOSE_FILES`.
Overrides are applied after the base stack, and after
`test/local-mode/docker-compose.codex.yml` for the Codex target:

```sh
LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-review \
LOCAL_MODE_COMPOSE_FILES=/tmp/fleetdb-review.yml \
make local-mode-up
```

Image tags default to the Compose project name for parallel builds. Override
`LOCAL_MODE_FLEETDB_IMAGE`, `LOCAL_MODE_LOOM_IMAGE`, or
`LOCAL_MODE_LOOM_CODEX_IMAGE` only when a run needs explicit image tags.

Verify the resolved checkout and project identity:

```sh
make local-mode-info
```

Run ID and `started_at` identify a proof epoch minted by a fresh container
start. A same-container restart resumes that epoch from its recovery journal;
a recreated container must mint a new run ID. `make local-mode-verify` reads the
live manifest, while `local-mode-info` does not mint or report candidate run
metadata.

Verify the container has bash through the resolved Compose project. Set
`project=loomcli-local-mode-b` instead when inspecting an explicit parallel
stack:

```sh
project="$(make -s local-mode-info | sed -n 's/^compose_project=//p')"
podman compose -p "$project" -f test/local-mode/docker-compose.yml \
  exec -T loom-local bash -lc 'echo "$SHELL"; command -v bash'
```

## Expected E2E Flow

`make local-mode-codex-up` should create:

- Workspace: `LOCALMODE`
- Planner agent: `codex-planner`
- Coder agent: `codex-coder`
- Planning task: FleetDB ID recorded as `plan_task_id` in the run manifest
- Approved coding task: FleetDB ID recorded as `code_task_id` in the run manifest
- Source repo: `/workspace/source-repo`
- Workspace repo: `/root/.loom/workspaces/LOCALMODE/source-repo`
- Agent worktrees under
  `/root/.loom/workspaces/LOCALMODE/worktrees/source-repo/`

The expected run is:

1. `codex-planner` starts through `loom daemon`.
2. It claims the manifest's planner task.
3. It runs Codex CLI through the supervised backend path.
4. Loom records an agent session in FleetDB with the manifest planner task ID.
5. The planner writes a design and moves the task to review.
6. `codex-coder` starts through the same daemon path.
7. It claims the manifest's coder task, which already has an approved design.
8. It writes `local-mode-agent-output.txt`, commits in its worktree, and
   closes the task.
9. Loom finalizes the FleetDB agent session with transcript, log, diff, and
   status metadata.

## UI Evidence

Use the UI to validate the product behavior, not just process exit codes.

In the Kanban board:

- The task titled `Local mode planner dogfood [run:<id>]` should leave
  planning evidence and move to review.
- The task titled `Local mode coder dogfood [run:<id>]` should close after the
  coder completes.

In the left agent panel:

- Active agents show while the daemon process is running them.
- Completed agents may show `Idle`. That is availability, not run history.
- Historical evidence belongs in the task Runs tab.

In a task Runs tab:

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
make local-mode-verify
```

The verifier prints the manifest task IDs before polling. Use those IDs for
additional manual session API calls; do not substitute old fixed IDs.

Check the container logs:

```sh
make local-mode-logs
```

Check the worktree inside the container:

```sh
project="$(make -s local-mode-info | sed -n 's/^compose_project=//p')"
podman compose -p "$project" -f test/local-mode/docker-compose.yml \
  exec -T loom-local bash -lc \
  'cd /root/.loom/workspaces/LOCALMODE/worktrees/source-repo/codex-coder && git log --oneline -5 && git status --short'
```

## Troubleshooting

### UI Shows Agents As Idle

The left panel shows current agent availability from the monitor API. After
the planner or coder exits, `Idle` can be correct. Open the task Runs tab
to inspect the completed run, transcript, logs, diff, and files.

### Runs Tab Is Empty

This means the daemon did not publish a FleetDB `agent-sessions` record with
the claimed task ID, or the UI/API could not query it. Check:

```sh
make local-mode-verify
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
LOCAL_MODE_CODEX_HOME=<codex-home> make local-mode-codex-up
```

### Port Conflicts

Defaults are:

```text
FleetDB: 8280
Loom API: 8282
Web UI: 8283
```

Override all three ports when running multiple stacks.
Also set `LOCAL_MODE_COMPOSE_PROJECT` so Compose container names and volumes
do not collide with the default stack.

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
