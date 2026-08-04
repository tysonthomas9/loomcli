# Local Mode Dogfood Stack

> **Status:** Current · *audited 2026-07-24*. Make targets, ports, and env knobs
> checked against `Makefile:162-228` and `test/local-mode/docker-compose.yml`.

This stack is the first shippable slice from the local-mode product docs:
one machine, shared filesystem, one Loom server, and supervised local agent
processes.

Full E2E runbook: `../../docs/testing/local-mode-podman-e2e.md`

Run it from the repo root:

```sh
make local-mode-up
```

Four variants share the same base compose file, each layering one override
(`Makefile:168-195`):

| Target | Agent backend | Extra requirement |
|---|---|---|
| `make local-mode-up` | `loom-backend-localdogfood` (deterministic) | none |
| `make local-mode-codex-up` | real `codex` CLI | `~/.codex` auth on the host |
| `make local-mode-claude-up` | real `claude` CLI | host `CLAUDE_HOME` (default `~/.claude`) mounted read-only; `~/.claude/.credentials.json` is often stale — see `docker-compose.claude.yml:10-15` for the fresh-token recipe and `LOCAL_MODE_CLAUDE_HOME` |
| `make local-mode-daytona-up` | daemon TS leaf routed to a Daytona sandbox | `DAYTONA_API_KEY`, a network-reachable `DAYTONA_REPO_URL` (`Makefile:186-195`) |

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
make local-mode-verify            # test/local-mode/verify-local-mode.sh
make local-mode-codex-verify      # same verifier, defaulted to the Codex stack's task ids
make local-mode-routing-verify    # test/local-mode/verify-agent-routing.py
make local-mode-webhook-verify    # test/local-mode/verify-webhook.sh
make local-mode-logs
make local-mode-down
```

`make local-mode-routing-verify` asserts role-based task routing for
UI-registered plan/task agents: it seeds a no-design task (must go to the plan
agent) and a designed task (must go to the task agent), exercises the UI
`POST /agents` endpoint, and checks the claims. Pair it with
`LOOM_DAEMON_LEAF=ts make local-mode-codex-up` to prove UI agent creation maps
to the TS execution path (`Makefile:214-215`).

`make local-mode-webhook-verify` is the real-stack E2E for the trigger-driven
GitHub webhook path: it signs a `pull_request.opened` delivery, asserts the
durable TriggerEvent / Delivery / DriverRun records, and checks that
redelivery is idempotent. Needs a running stack plus curl, openssl, and
python3 (`Makefile:221-222`).

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

## Related

- [`../../docs/testing/local-mode-podman-e2e.md`](../../docs/testing/local-mode-podman-e2e.md)
  — the full E2E runbook for this stack
- [`../../docs/product/local-mode-product-spec.md`](../../docs/product/local-mode-product-spec.md)
  — the product spec this stack is the first slice of
- [`../../docs/testing/README.md`](../../docs/testing/README.md) — index of all
  test surfaces
- [`../playground/README.md`](../playground/README.md) — the cheap single-host
  daemon failure-mode harness; use it instead when you are testing the
  supervisor, not the product
- [`../../deploy/podman-stack/README.md`](../../deploy/podman-stack/README.md) —
  the distributed-topology stack (this one is single-machine)
- [`../../docs/loom-glossary.md`](../../docs/loom-glossary.md) — **local mode**
  is not **local-only workspace**, and neither is **fleet mode**
