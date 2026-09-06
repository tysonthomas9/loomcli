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
`codex-coder`. The image also bakes `codex-requirements.toml` into
`/etc/codex/requirements.toml`: the pre-turn `loom skill materialize`
hook runs with managed provenance (no interactive `/hooks` review), and
`allow_managed_hooks_only` refuses unmanaged hooks, including any
`.codex/hooks.json` inside cloned task repos.

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

## PostgreSQL committed workspace bootstrap

`make local-mode-postgres-up` layers `docker-compose.postgres.yml` onto the
standard deterministic local-mode stack. It adds PostgreSQL 16 with a
project-scoped volume, retains Redis for auxiliary services, and selects Fleet's
`--backend postgres --pg-committed-workspace-creation` path. The paired Fleet
build must support that flag. The normal entrypoint creates the workspace,
agents and tasks through product APIs; this override inserts no database state.

Use a new project and free ports so workspace creation runs against fresh storage:

```sh
LOCAL_MODE_COMPOSE_PROJECT=loom-pg-sse-owned \
LOCAL_MODE_FLEETDB_PORT=8580 LOCAL_MODE_API_PORT=8582 LOCAL_MODE_UI_PORT=8583 \
LOCAL_MODE_COMPOSE_UP_FLAGS='--build -d' make local-mode-postgres-up
```

The default Fleet build context is the sibling `fleet-db` checkout. Set
`LOCAL_MODE_FLEETDB_BUILD_CONTEXT` to an absolute path to test another paired
checkout. Existing image, compose-engine and extra-override knobs apply. The
PostgreSQL port is not published; its fixed credentials are disposable local
fixture credentials, not deployment configuration.

This path uses `localdogfood`, not a paid AI backend. Successful startup is not
proof of SSE delivery or autonomous task completion. Use the paired public issue routing changes for claim/release; other lifecycle
routing remains incomplete. Report daemon failures rather than substituting
manual locks or synthetic outcomes. Browser verification must record actual
stream requests and UI updates separately from startup.

After evidence capture, remove only this run's resources using the same project
and any extra override files:

```sh
LOCAL_MODE_COMPOSE_PROJECT=loom-pg-sse-owned make local-mode-postgres-down
```

This removes the named project's volumes. Do not use an existing shared project
or reuse a previously populated workspace as clean-genesis proof.

Workspace-local repository names (for example `source-repo`) are first-class
Fleet repo records, not global `org/repo` ownership keys. Loom's RepoStore creates
and deletes those records only; workspace configuration and worktree resolution
load them through RepoStore.List. It must not PATCH the global workspace catalog
with bare names or compensate a catalog rejection by deleting a successfully
created repository. Git remote URLs and local paths are not coerced into global
catalog identities.


### Real PostgreSQL browser regression

Use a dedicated Podman project whose name starts with `loomcli-pg-browser-`.
After `local-mode-postgres-up` reports the product entrypoint ready, run:

```sh
LOCAL_MODE_COMPOSE_PROJECT=loomcli-pg-browser-owned \
LOCAL_MODE_API_PORT=8582 LOCAL_MODE_UI_PORT=8583 \
make local-mode-postgres-sse-verify
```

Use the same project/ports for startup and teardown. This target **stops and
restarts that project's UI proxy** to sever the existing fetch-SSE sockets while
leaving the API available for mutations. The test checks the container's Compose
ownership label and restores it in `finally`; a process kill can still require
manual restoration of this run-owned proxy. Do not point it at shared services.
The currently paired Fleet must implement enrolled public issue command routing;
a 500 from claim/release is a failed prerequisite, not a skipped success.

The tests observe the application's actual Fetch response bytes through Chromium
CDP. They neither replace browser responses nor create another SSE subscription.
They require connected frames, scoped mutation IDs, immediate rendering before
collection responses, later authoritative projection refresh, matching deliveries
to two independent browser contexts, exact resume headers, and no document reload
through the real outage. Duplicate/gap assertions cover the deliberately created
mutation sequence and observation interval, not arbitrary workloads.

`PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH` can select an already installed compatible
Chromium. Otherwise use the repo's installed Playwright browser. A non-secret
local-mode API-key value prevents the harness reading the host's key file; local
mode disables auth, so this target provides no authentication-security proof.
Results, sanitized CDP evidence and browser screenshots are attached to the JSON
report at `internal/webui/frontend/test-results/pg-sse-report.json`; set
`PLAYWRIGHT_JSON_OUTPUT_FILE` to preserve it elsewhere. Do not publish general
HAR files or host credentials. No reload fallback, retry-on-failure test rerun or
manual database enrollment is permitted to turn a failure into a pass.

The SSE gate creates fresh workspaces through actual POST201 responses and verifies
that they have no agents. Existing localdogfood agents remain in their original
workspace; they cannot claim the suite's tasks. Each spec uses explicit workspace
IDs and the existing source repository (`LOOM_SSE_TEST_SOURCE_REPO`, default
`/workspace/source-repo`). Tests close their tasks; matching owned-project teardown
removes the temporary workspaces and volumes. Do not substitute arbitrary issue
types or relax event-count assertions to avoid autonomous-worker interference.

The [final paired proof](../../docs/testing/postgres-sse-regression-proof.md) records
seven real browser passes, exact traces and remaining architecture limits.
