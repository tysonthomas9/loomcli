# Local Mode Dogfood Stack

This stack is the first shippable slice from the local-mode product docs:
one machine, shared filesystem, one Loom server, and supervised local agent
processes.

Full E2E runbook: `../../docs/testing/local-mode-podman-e2e.md`

Run it from the repo root:

```sh
make local-mode-up
```

To run the same stack with real Codex CLI agents:

```sh
make local-mode-codex-up
```

The Codex variant builds Codex into the Loom container, mounts
`${HOME}/.codex` read-only at `/codex-host`, copies `auth.json` and
`config.toml` into the container, and registers `codex-planner` plus
`codex-coder`.

Open:

- UI: http://localhost:8283/ws/LOCALMODE/kanban
- API: http://localhost:8282
- fleet-db: http://localhost:8280

What starts:

- `fleet-db`: shared control plane and issue store.
- `loom-local`: creates a local workspace, source repo, planner/coder
  worktrees, daemon profile, agent definitions, seeded tasks, `loom serve`,
  and a workspace-daemon manager.
- `ui-local`: Caddy serving `internal/webui/frontend/dist` from the host and
  proxying API traffic to `loom-local`.
- `loom-backend-localdogfood`: deterministic external backend used by the
  two agents so the run does not require Codex credentials.
- Codex variant: real `codex-planner` and `codex-coder` agent definitions
  using the same daemon/session path as the deterministic backend.

The daemon manager keeps one workspace-scoped `loom daemon` running for every
local workspace that has at least one `auto=true` agent assignment. It scans
FleetDB periodically, so a workspace created later through the CLI or UI can
start picking up work without restarting the stack.

Expected dogfood flow:

- `LOCALMODE-1` is the seeded epic lane.
- `local-planner` claims `LOCALMODE-2`, writes a design, and moves it to review.
- `local-coder` claims `LOCALMODE-3`, writes and commits
  `local-mode-agent-output.txt`, then closes the task.
- The task Sessions tab should show daemon-created sessions with logs,
  transcript presence, diff stats, and final status after each run exits.

Useful commands:

```sh
make local-mode-verify
make local-mode-codex-verify
make local-mode-evals-verify
make local-mode-logs
make local-mode-down
```

`make local-mode-verify` polls the running stack and asserts that the seeded
planner/coder tasks completed the daemon path, recorded sessions, exposed
transcripts, and produced the coder diff artifact. Override
`LOCAL_MODE_API_URL`, `LOOM_WORKSPACE`, `LOOM_LOCAL_MODE_PLAN_TASK_ID`, and
`LOOM_LOCAL_MODE_CODE_TASK_ID` when verifying a non-default stack.
Use `make local-mode-codex-verify` after `make local-mode-codex-up`; it
defaults the verifier to the Codex stack's seeded `LOCALMODE-2` and
`LOCALMODE-3` tasks.

`make local-mode-evals-verify` runs the standing session-eval dogfood path
against a running stack. The default mode is the deterministic plain stack:

```sh
make local-mode-up
make local-mode-evals-verify
```

For the full live loop, start the Codex stack and opt into Codex mode:

```sh
make local-mode-codex-up
LOCAL_MODE_EVALS_MODE=codex make local-mode-evals-verify
```

The plain mode proves eval workflow provisioning, the codex-less preflight
defer path, and the Traces read path without judge spend. The Codex mode is
live: it uses mounted Codex auth and can spend judge tokens while asserting the
session-evals record, session `eval_status=done`, eval rollup, and optional UI
surfaces when `agent-browser` is available.

The stack uses Docker/Podman volumes, so sessions and workspace files survive
container restarts until `make local-mode-down` removes the stack volumes.

Codex variant knobs:

The Codex image installs the current npm `latest` release by default. Set
`LOCAL_MODE_CODEX_CLI_VERSION` only when a reproducible version pin is needed.

```sh
LOCAL_MODE_CODEX_HOME=<codex-home> make local-mode-codex-up
LOCAL_MODE_CODEX_CLI_VERSION=0.144.1 make local-mode-codex-up
```

Compose and parallel-stack knobs:

```sh
LOCAL_MODE_COMPOSE="docker compose" make local-mode-up
LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-b \
LOCAL_MODE_FLEETDB_PORT=8380 \
LOCAL_MODE_API_PORT=8382 \
LOCAL_MODE_UI_PORT=8383 \
LOCAL_MODE_COMPOSE_UP_FLAGS="--build -d" \
make local-mode-up

LOCAL_MODE_API_PORT=8382 make local-mode-verify
```

Use the same project name for logs and teardown:

```sh
LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-b make local-mode-logs
LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-b make local-mode-down
```

Additional Compose overrides are appended after the base file, or after the
Codex override for `make local-mode-codex-up`:

```sh
LOCAL_MODE_COMPOSE_FILES=/tmp/fleetdb-review.yml make local-mode-up
```

The local-mode image can materialize built-in Flue workflows. Startup now
preflights the sibling Flue checkout before compose starts. By default Make
looks at `../flue` from the repo root, then exports an absolute `FLUE_REPO` to
compose; direct compose users should use the compose-file-relative default
`../../../flue` or pass an absolute `FLUE_REPO`. The checkout must have
`packages/runtime/node_modules/hono`,
`packages/runtime/node_modules/@hono/node-server`, and a built
`packages/cli/dist/flue.js`.

To force a compatible sibling FleetDB checkout for session-evals,
artifact-content, and list-extension support, use the documented override:

```sh
FLEET_DB_REPO=/absolute/path/to/fleet-db \
LOCAL_MODE_COMPOSE_FILES=test/local-mode/docker-compose.fleetdb-sibling.yml \
make local-mode-up
```

If `FLEET_DB_REPO` is omitted, the override builds from `../../../fleet-db`
relative to `test/local-mode/docker-compose.fleetdb-sibling.yml`, which is the
sibling `../fleet-db` checkout from the repo root.

Image tags default to the Compose project name for parallel builds. Override
`LOCAL_MODE_FLEETDB_IMAGE`, `LOCAL_MODE_LOOM_IMAGE`, or
`LOCAL_MODE_LOOM_CODEX_IMAGE` only when a run needs explicit image tags.

Troubleshooting:

- On macOS Apple Silicon, Podman 5.8.x can report `podman machine start`
  success while `podman machine list` still shows `LAST UP: Never` and the VM
  console log enters CoreOS emergency mode with `systemd-fsck-root` UUID
  errors. This is a Podman machine boot failure, not a local-mode app failure.
  Recreate or downgrade/fix the Podman machine before running
  `make local-mode-up`, or use Docker Compose when available.
