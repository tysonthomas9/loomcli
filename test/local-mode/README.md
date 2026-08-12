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

To exercise unified prompt/scripted-agent creation as well as the Codex
execution backend, use the workflow-authoring profile:

```sh
make local-mode-codex-workflows-up
```

Embedded workflow source is built into a registered driver on first use. That
requires Loom's local SDK plus the pinned Flue CLI/runtime checkout, so this
target adds `docker-compose.workflow-build.yml` and checks the toolchain before
starting Compose. The default Flue path is `../dynamic-workflows/flue`
relative to this checkout directory. It must be at the commit in
`internal/workflows/FLUE_COMMIT`, with pnpm dependencies installed and the
CLI/runtime built. Override it when needed:

```sh
FLUE_SRC=/path/to/flue make local-mode-codex-workflows-up
```

The mounted Flue checkout executes inside a Debian/glibc container. A normal
pnpm install on macOS can therefore look complete while omitting Rolldown's
Linux native binding. The startup preflight checks the binding for every
installed Rolldown version before Compose runs. It maps `arm64`/`aarch64` to
`@rolldown/binding-linux-arm64-gnu` and `amd64`/`x86_64` to
`@rolldown/binding-linux-x64-gnu`; set `LOCAL_MODE_CONTAINER_ARCH` when the
Compose target CPU differs from the host.

If the preflight reports a missing binding, install the current host and Linux
container optional dependencies from the pinned Flue checkout. For an arm64
container:

```sh
export XDG_CONFIG_HOME="${TMPDIR:-/tmp}/loom-flue-pnpm-linux-arm64-gnu"
pnpm config set --global supportedArchitectures '{"os":["current","linux"],"cpu":["current","arm64"],"libc":["current","glibc"]}'
pnpm install --frozen-lockfile --force --filter @flue/cli... --filter @flue/runtime...
```

Use `x64` instead of `arm64` for an amd64/x86_64 container. The temporary
`XDG_CONFIG_HOME` keeps this cross-platform pnpm setting out of the developer's
normal global configuration.

The narrower `local-mode-codex-up` profile remains useful for supervised-agent
and daemon testing without a Flue source checkout. If prompt-agent creation is
attempted there before its driver has been registered, the API returns 503
without persisting a partial Role or AgentService.

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

- The stack reuses or creates a seeded epic lane.
- Every `make local-mode-*-up` invocation mints a run ID and creates a planner
  task plus coder task whose titles include `[run:<id>]`.
- `local-planner` claims the manifest's planner task, writes a design, and
  moves it to review.
- `local-coder` claims the manifest's coder task, writes and commits
  `local-mode-agent-output.txt`, then closes the task.
- The task Runs tab should show daemon-created sessions with logs,
  transcript presence, diff stats, and final status after each run exits.

Useful commands:

```sh
make local-mode-verify
make local-mode-codex-verify
make local-mode-logs
make local-mode-down
```

`make local-mode-verify` reads `/tmp/loom-local-mode-run.json` from the running
`loom-local` container, then polls only the task IDs recorded in that manifest.
The container captures `started_at` immediately before seeding, on the same VM
clock FleetDB uses. Invalid timestamps and tasks or sessions older than that
threshold fail closed, so old volume data cannot satisfy a new proof. Use
`make local-mode-codex-verify` after `make local-mode-codex-up`; that target
also requires the manifest backend to be `codex`. Override `LOCAL_MODE_API_URL`
when verifying a non-default port.

The stack uses Docker/Podman volumes, so sessions and workspace files survive
container restarts until `make local-mode-down` removes the stack volumes. A
marker inside `loom-data` binds those volumes to the physical checkout and
Compose project. Startup fails closed rather than adopting an unmarked legacy
volume or a volume created by another checkout.

Codex variant knobs:

The Codex image installs the current npm `latest` release by default. Set
`LOCAL_MODE_CODEX_CLI_VERSION` only when a reproducible version pin is needed.

```sh
LOCAL_MODE_CODEX_HOME=<codex-home> make local-mode-codex-up
LOCAL_MODE_CODEX_CLI_VERSION=0.144.1 make local-mode-codex-up
```

Compose and parallel-stack knobs:

By default, the Compose project and image tags include a stable hash of the
physical checkout path. `make local-mode-info` prints the resolved source root,
checkout ID, and project. Run-specific identity comes from the live container
manifest, not from a separate `make local-mode-info` invocation. Different
checkouts therefore do not share volumes even when both use the default
targets. Host ports are still shared; override ports when running them
simultaneously.

```sh
LOCAL_MODE_COMPOSE="docker compose" make local-mode-up
LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-b \
LOCAL_MODE_FLEETDB_PORT=8380 \
LOCAL_MODE_API_PORT=8382 \
LOCAL_MODE_UI_PORT=8383 \
LOCAL_MODE_COMPOSE_UP_FLAGS="--build -d" \
make local-mode-up

LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-b \
LOCAL_MODE_API_PORT=8382 \
make local-mode-verify
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

Stacks created before checkout provenance was introduced have unmarked
volumes. Tear down the old project once with its original explicit name, for
example `LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode make local-mode-down`,
or choose a fresh project name. The entrypoint intentionally will not infer
ownership from stale data.

`local-mode-down` is necessarily project-name based: Compose cannot portably
read provenance from every stopped or unmarked legacy volume before deleting
it. An explicit `LOCAL_MODE_COMPOSE_PROJECT` therefore selects the volumes to
destroy. Confirm the exact project with `make local-mode-info` and repeat that
same value on verify, logs, and teardown.

Troubleshooting:

- On macOS Apple Silicon, Podman 5.8.x can report `podman machine start`
  success while `podman machine list` still shows `LAST UP: Never` and the VM
  console log enters CoreOS emergency mode with `systemd-fsck-root` UUID
  errors. This is a Podman machine boot failure, not a local-mode app failure.
  Recreate or downgrade/fix the Podman machine before running
  `make local-mode-up`, or use Docker Compose when available.
