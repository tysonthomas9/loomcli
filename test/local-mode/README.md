# Local Mode Dogfood Stack

This stack is the first shippable slice from the local-mode product docs:
one machine, shared filesystem, one Loom server, and supervised local agent
processes.

Full E2E runbook: `../../docs/testing/local-mode-podman-e2e.md`

Run it from the repo root, as your own project on free ports:

```sh
LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-<you> \
LOCAL_MODE_FLEETDB_PORT=8680 LOCAL_MODE_API_PORT=8682 LOCAL_MODE_UI_PORT=8683 \
make local-mode-up
```

Every `make local-mode-*-up` target runs `preflight.sh` first. It fails fast,
naming the knob, when `LOCAL_MODE_FLEETDB_PORT`, `LOCAL_MODE_API_PORT` or
`LOCAL_MODE_UI_PORT` (default 8280/8282/8283) is already listening. A re-up
of a project that already has containers skips the check.

The examples below omit the project and port knobs for brevity; add them to
each `up` command.

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
container restarts until `make local-mode-down` removes the stack volumes. That
includes everything in Redis: the fleet-db issue store and the terminal tab
metadata (`internal/webui/tabmeta`), because Redis persists to the `redis-data`
volume (`--appendonly yes`, plus Redis' default RDB snapshots).

Resetting state:

`make local-mode-down` runs `compose down -v`, which removes the stack volumes
and is the way to get a clean, freshly seeded board. A plain `restart` or a
`down`/`up` without `-v` deliberately preserves state — the entrypoint reuses
the existing workspace, epic, and seeded issues. That reuse assumes the volumes
stay in step; remove some but not all of them and startup can fail (see
Troubleshooting).

Agent API variant (OpenCode Leads on a scripted fake model):

`make local-mode-agents-up` runs the same stack, with `loom-local` built from
the `agents` target of `Dockerfile`, in the same build as its `local-mode`
stage (no separately tagged base image). It adds the pinned OpenCode
2.0.19 build (b30c4d0, built as `aft.yml` does; the first build takes several
minutes) and the AFT fake model (`tests/aft/fixtures/fake-model`), which
listens on 127.0.0.1:4010 inside the container. Serve's OpenCode talks only to
that model, so no provider login is needed. The workspace's `source-repo` is the
Slack-clone fixture (`scripts/seed-slack-clone.sh`). The default targets and
images are unchanged.

Always run it as your own project on unclaimed ports. Run `podman ps` and
`lsof -nP -iTCP -sTCP:LISTEN` first, and never touch another project's stack:

```sh
LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-<you> \
LOCAL_MODE_FLEETDB_PORT=8680 LOCAL_MODE_API_PORT=8682 LOCAL_MODE_UI_PORT=8683 \
LOCAL_MODE_COMPOSE_UP_FLAGS="--build -d" \
make local-mode-agents-up
```

- The Caddy service mounts the frontend dist from the host. The `up` targets
  rebuild it when it is missing or older than any frontend source.
- Under Podman on macOS, run from a path the VM shares (`/Users/...` or
  `/private/tmp/...`, not `/tmp/...`).
- From a git worktree, also point `fleet-db` at a fleet-db checkout through
  `LOCAL_MODE_COMPOSE_FILES` (an override setting `services.fleet-db.build.context`).

UI: `http://127.0.0.1:8683/ws/LOCALMODE/agents`. agent-browser on the host
reaches the published port directly. Use your own session and close only that
one (never `close --all`):

```sh
export AGENT_BROWSER_SESSION=<you>
agent-browser open http://127.0.0.1:8683/ws/LOCALMODE/agents
# + Add agent -> Lead -> Name -> AI Backend: OpenCode -> Create Agent
# opens /ws/LOCALMODE/chat/<agent_id>; send a message there
agent-browser close
```

Script the replies before you send. Each agent turn takes the next step; an
empty queue replies `ok`. A `bash` step runs OpenCode's shell tool, so
`{"bash":"sleep 120"}` keeps a turn running long enough to test Stop or a
restart:

```sh
C=loomcli-local-mode-<you>-loom-local-1
podman exec $C curl -s -X POST http://127.0.0.1:4010/__script \
  -d '{"steps":[{"text":"Hello from the fake model"},{"bash":"sleep 120"}]}'
podman exec $C curl -s http://127.0.0.1:4010/__requests   # requests seen so far
podman exec $C curl -s -X POST http://127.0.0.1:4010/__reset
```

OpenCode's data lives on the `loom-data` volume, so `podman restart $C`
mid-turn resumes the agent with its history. The fake model's queue is lost
on restart, so re-script it once the restart returns. Tear down only your own
project; this also removes its volumes:

```sh
LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-<you> make local-mode-agents-down
```

Real-model mode: `LOCAL_MODE_AGENTS_REAL=1` adds
`docker-compose.agents-real.yml` and does not start the fake model, so Leads
use the providers you are logged in to with `opencode auth login` on the host.
OpenCode 2.x keeps that login in `opencode.db` (the legacy `auth.json` is a
stale one-time import source and is not copied).

The host's OpenCode folder `~/.local/share/opencode` (or
`LOCAL_MODE_OPENCODE_DATA`) is never mounted into the stack, read-write or
read-only. Instead, each REAL stack gets a private copy of the login:

- Before `up`, `real-opencode-copy.sh` takes one SQLite online backup of the
  host's `opencode.db`, opened read-only (nothing is created or written in the
  host folder), into
  `~/.local/state/loom-local-mode/<project>/opencode.db` (`LOCAL_MODE_STATE_DIR`
  overrides `~/.local/state/loom-local-mode`), a mode-600 file in a mode-700
  folder that belongs to that compose project only. It then runs
  `PRAGMA quick_check` on the copy. If the backup or the check fails, `up`
  stops with a clear message and boots nothing; it never retries in another
  mode. That includes a host database whose `-wal`/`-shm` pair is damaged or
  unreadable (for example `file is not a database`): the stack refuses to boot
  until you repair the host database yourself, with OpenCode closed. A re-up of
  the same project keeps its existing copy.
- The container mounts only that copy, read-only, and on first boot seeds its
  own OpenCode database on the `loom-data` volume from it. A `podman restart`
  keeps the stack's database.
- Nothing is written back. Stack sessions never reach your host history, and
  token refreshes stay inside the stack. Because OAuth providers such as
  OpenAI rotate the refresh token, a refresh on one side can sign the other
  out, so a re-login is needed now and then: run `opencode auth login` on the
  host, then `make local-mode-agents-down` and `up` again for a fresh copy (a
  restart reuses the old one).
- `make local-mode-agents-down` (and `make local-mode-down`) remove that
  project's copy along with its volumes. Other projects' copies are untouched.
- Several REAL stacks may run at once, and host OpenCode may keep running.
  When host OpenCode has closed the database cleanly (no `opencode.db-wal`),
  the copy reads the main file as immutable and refuses if it changes during
  the copy; just run `up` again.

The copy holds the login: never print, open or share it.

The create flow's model list comes
from those providers. To set a default model, use `LOCAL_MODE_AGENTS_MODEL`
(`provider/model`); otherwise OpenCode picks one. The fake-model mode stays
the default for AFT and CI.

Before printing `[local-mode] ready`, a real stack starts OpenCode and waits
(up to `LOCAL_MODE_AGENTS_WARM_TIMEOUT`, default 90s) until its model catalog
lists `LOCAL_MODE_AGENTS_MODEL`, or a default model when that is unset. It
logs the model count and the time taken. On timeout it warns, naming the
providers it saw, and comes up anyway. The `loom-local` healthcheck waits for
that ready line, and with `-d` in `LOCAL_MODE_COMPOSE_UP_FLAGS`,
`make local-mode-agents-up` returns only after it, printing `STACK UP`.

```sh
LOCAL_MODE_AGENTS_REAL=1 LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-<you> \
LOCAL_MODE_FLEETDB_PORT=8680 LOCAL_MODE_API_PORT=8682 LOCAL_MODE_UI_PORT=8683 \
LOCAL_MODE_COMPOSE_UP_FLAGS="--build -d" make local-mode-agents-up
```

Tear it down with the same `make local-mode-agents-down` as above. If the
login is revoked or expired, run `opencode auth login` on the host, then down
and up the stack. Never log in inside the container, and never print or copy
`opencode.db` yourself.

The agents image also carries the R21 pinned `codex` 0.157.1 and `claude`
2.1.285 on `PATH`. Real mode mounts the host's codex `~/.codex/auth.json`
(`LOCAL_MODE_CODEX_AUTH`) and Claude `~/.claude/.credentials.json`
(`LOCAL_MODE_CLAUDE_AUTH`) READ-ONLY inside `CODEX_HOME=/root/.loom/agents-codex`
and `CLAUDE_CONFIG_DIR=/root/.loom/agents-claude`. Both roots, with everything
else the CLIs write (config, sessions, caches), live on the `loom-data` volume,
so `make local-mode-agents-down` removes them. A terminal-tab `codex` or
`claude` uses your logins. All three files must exist on the host. At start
the entrypoint prints a WARNING for a login that fails its CLI's own status
check; check by hand the same way, never by reading the file:

```sh
podman exec $C codex login status
podman exec $C claude auth status
```

The same refresh caveat applies: codex refreshes a ChatGPT login after it
ages, and the read-only mount refuses the write, so use a recently refreshed
host login (run `codex` on the host first). On macOS the Claude login lives in
the Keychain and `~/.claude/.credentials.json` is often stale. Use a
long-lived token instead:

1. On the host, run `claude setup-token` and finish the browser sign-in.
2. Save the printed token, and nothing else, to `~/.config/loom/claude-token`
   in an editor (not via `echo`, which keeps it in shell history), then
   `chmod 600 ~/.config/loom/claude-token`.
3. Start the stack with
   `LOCAL_MODE_CLAUDE_TOKEN_FILE=$HOME/.config/loom/claude-token` added to the
   `make local-mode-agents-up` line above.

The file is mounted read-only at `/run/secrets/claude-token`. The image's
`claude` wrapper reads it into `CLAUDE_CODE_OAUTH_TOKEN` for each claude
process only; serve, terminal shells and logs never see it. Because Claude's
first-run onboarding ignores the token and ends at "Select login method", the
entrypoint writes `{"hasCompletedOnboarding":true}` to the volume's
`.claude.json` when a token is mounted and that file does not exist yet. With it unset,
the `.credentials.json` bind is used as before.

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

Each built image carries the label `io.loom.local-mode.project=<project>`, so
two projects built from the same sources get different image IDs. Removing
one project's image (`podman rmi <project>-loom:latest`) never removes
another project's. Each image is built in one step (`loom-local` with
`--target local-mode` or `--target agents`), so a `podman image prune` that
runs between two make steps cannot delete a base image that a later step
needs.

Troubleshooting:

- On macOS Apple Silicon, Podman 5.8.x can report `podman machine start`
  success while `podman machine list` still shows `LAST UP: Never` and the VM
  console log enters CoreOS emergency mode with `systemd-fsck-root` UUID
  errors. This is a Podman machine boot failure, not a local-mode app failure.
  Recreate or downgrade/fix the Podman machine before running
  `make local-mode-up`, or use Docker Compose when available.
- All terminal tabs gone after a host or Docker/Podman restart: on a stack
  created before Redis persistence was enabled, Redis ran with `--appendonly no
  --save ""` and came back empty after every container restart. Recreate the
  stack (`make local-mode-down && make local-mode-up`) so Redis starts with AOF
  enabled. The same reset is the recovery if Redis ever fails to start from a
  damaged AOF.
- Startup exits with `workspace "LOCALMODE" already exists`: `loom-data` was
  removed while `redis-data` (which holds the workspace record) survived. Reset
  both together with `make local-mode-down` (it is `down -v`), then re-run
  `make local-mode-up`.
