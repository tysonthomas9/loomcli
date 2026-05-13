# Playground

Self-contained workspace + mock agent harness for exercising loom end-to-end
without LLM calls or touching real source code.

`setup.sh` creates a throwaway git repo, registers it as a loom workspace,
defines `plan` + `task` agents that dispatch to `loom-backend-playground`
(a deterministic shell script), and seeds a few sample tasks. From there you
can drive the daemon with `loom monitor`, run `loom lead` interactively, hit
the web UI, etc.

## Prereqs

- `loom` on `PATH`.
- `loom serve` already running on `http://localhost:8080` (or the Loom Desktop
  app), so the daemon spawned by `loom workspace ops ensure-runtime` has a
  backend to talk to.

## Up / down

```sh
# 1. From this dir:
./setup.sh

# 2. In a separate terminal (do this in every shell that should talk to the
#    playground — it puts loom-backend-playground on PATH and pins the workspace):
source test/playground/.runtime/env

# 3. Start the agent supervisor:
loom daemon            # foreground; Ctrl+C to stop

# 4. Watch the run (optional, another terminal):
loom monitor           # or: loom data list

# Teardown:
./test/playground/teardown.sh
```

After ~30 seconds you should see the 3 seeded tasks move `open → closed`,
with one `Playground implementation (PLAYGROUND-N)` commit per task in
the coder worktree under `~/.loom/workspaces/playground/worktrees/repo/playground-coder/`.

`./teardown.sh` is idempotent and reliably purges the workspace from
fleet-db — including the operational data keys (`fleet-db:PLAYGROUND:*`)
that `loom workspace remove` leaves behind. `./setup.sh` also self-heals:
if it hits an HTTP 409 "already exists" on workspace create (orphan keys
from an earlier interrupted run), it transparently runs teardown and
retries once. Net effect: you can `./setup.sh` repeatedly without
manually nuking `~/.loom` between runs.

Under the hood, `teardown.sh` connects to fleet-db's embedded Redis
(address from `~/.loom/fleet-db/runtime.json`) and SCAN-then-DEL's every
`fleet-db:PLAYGROUND:*` key. Only PLAYGROUND keys are touched; other
workspaces in the same fleet-db are unaffected.

## What gets created

| Thing                              | Where                                   |
| ---------------------------------- | --------------------------------------- |
| Mini git repo (initial commit)     | `.runtime/repo/`                        |
| Backend binary on PATH             | `.runtime/bin/loom-backend-playground`  |
| Loom workspace                     | `playground` (in fleet-db + `~/.loom/`) |
| Worktree(s) for the playground repo | `~/.loom/workspaces/playground/...`     |
| Agent assignments                  | `playground-planner` (plan), `playground-coder` (task) |
| Seed tasks                         | 3 tasks: "Seed task 1/2/3 (playground)" |

## Where to look

- `loom monitor` — agent + task dashboard for the active workspace.
- `loom data list` — issue list.
- `loom data show <id>` — full record for one task (design, comments).
- `~/.loom/workspaces/playground/sessions/<session-id>/agent_transcript.jsonl`
  — one line per assistant message written by the mock harness.
- `git -C .runtime/repo log --oneline` — the initial commit plus one
  `Playground implementation (...)` commit per task the coder closed.

## How the mock decides what to do

The harness reads `LOOM_ROLE` from its environment (set by `loom daemon`
based on the agent assignment's role):

- `LOOM_ROLE=plan` → claim task, write a canned design, set status `open`
  (with the design attached — a playground shortcut that skips the human
  `loom lead` approval step the real flow uses), call `loom complete`.
- `LOOM_ROLE=task` → claim task, append to `playground.txt`, git commit,
  close task, call `loom complete`.

Other env vars: `LOOM_ASSIGNED_TASK_ID`, `LOOM_AGENT_NAME`,
`LOOM_SESSION_ID`, `LOOM_WORKSPACE_RUNTIME_DIR`. Override the per-step delay
with `LOOM_PLAYGROUND_STEP_DELAY` (default 1 second) when you want the
monitor UI to keep up visually.

## Comparison to `test/local-mode/`

`test/local-mode/` brings up a full Docker/Podman stack (fleet-db, daemon,
web UI) for distributed-mode dogfooding. The playground here is single-host
and assumes you already have `loom serve` running locally — useful for
manual exploration, demoing, and quick sanity checks rather than full
stack regression testing.

## Customizing the mock

Edit `loom-backend-playground` and re-run `setup.sh` (the harness is invoked
by the daemon every time it starts an agent run; no rebuild needed). To
exercise different scenarios — failures, conflicts, multiple file edits —
extend `run_planner` / `run_coder`, or add new role branches. The harness
just needs to read its prompt from stdin (it currently ignores it) and emit
JSON-stream output / `loom data ...` calls.

To edit the seed repo's starting state, change files under `repo-template/`
and `./teardown.sh && ./setup.sh`.

## Failure-mode harness

`test/playground/` doubles as a daemon-lifecycle test harness. While
`loom-backend-playground` exercises the happy path, the sibling backends
below misbehave in named, reproducible ways so scenarios can verify the
daemon's watchdog, orphan sweep, retry/backoff, and classification paths
without LLM calls.

### Failure-mode backend zoo

| Backend                                | Failure mode                                                                                                          | Tests                                                                                              |
| -------------------------------------- | --------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `loom-backend-playground-hang`         | Parent goes silent. No descendants.                                                                                   | Basic watchdog (single-pgroup case). Simplest template for "copy this when adding a new bug."      |
| `loom-backend-playground-grandchild`   | Spawns setsid grandchild in its own pgroup; mode flag (`hang`/`orphan`) selects whether the parent hangs or exits 0.  | PR #63: descendant-pgroup signal during watchdog kill, and startup orphan sweep.                   |
| `loom-backend-playground-crash`        | Exits non-zero a few seconds after invoke.                                                                            | Failure classification + retry/backoff.                                                            |
| `loom-backend-playground-slow`         | Writes one stdout line every `interval` seconds (less than the watchdog timeout).                                     | Regression guard: legitimate slow work must NOT trigger the watchdog.                              |

Each backend implements the same `meta`/`health`/`invoke` contract as
`loom-backend-playground` and self-documents its failure mode in a header
comment.

### Running a scenario

```sh
# List available scenarios
./run_scenario.sh

# Run one
./run_scenario.sh slow_backend_not_killed
```

`run_scenario.sh` is a thin wrapper. Scenarios in `scenarios/*.sh` are
self-contained — they handle their own setup, daemon lifecycle, assertions,
and teardown via the same `setup.sh`/`teardown.sh` that drive the happy
path, just with a scenario argument:

```sh
bash test/playground/scenarios/slow_backend_not_killed.sh
# or directly:
./setup.sh slow && ... && ./teardown.sh slow
```

Exit codes: 0 pass, 1 assertion failure, 2 prereq missing, 3 timeout.

### Writing a new scenario

Copy a sibling in `scenarios/` and edit it. The shape is:

```sh
#!/usr/bin/env bash
set -euo pipefail
HERE="$(cd "$(dirname "$0")/.." && pwd)"
SCENARIO_NAME="<backend suffix>"     # picks loom-backend-playground-<this>
PLAYGROUND_LOG_SCOPE="<short label>" # appears in [scope HH:MM:SS] log lines
export PLAYGROUND_LOG_SCOPE

. "$HERE/lib/common.sh"
. "$HERE/lib/proctree.sh"
. "$HERE/lib/daemon.sh"

cleanup() {
  local rc=$?
  stop_daemon_graceful || true
  "$HERE/teardown.sh" "$SCENARIO_NAME" >/dev/null 2>&1 || true
  exit $rc
}
trap cleanup EXIT

"$HERE/setup.sh" "$SCENARIO_NAME"
# shellcheck disable=SC1091
. "$HERE/.runtime-$SCENARIO_NAME/env"

# create task, start daemon, assert, exit 0 / EXIT_FAIL / EXIT_TIMEOUT
```

The script header **must** document:

1. The bug or regression the scenario guards against (link to PR/issue).
2. Expected outcome on HEAD (pass).
3. Expected outcome on a named pre-fix commit (fail) — the negative
   control.

### Adding a new failure-mode backend

1. Copy `loom-backend-playground-hang` to `loom-backend-playground-<mode>`.
2. Edit the header to describe the failure mode and link the bug/PR it
   guards against.
3. Edit `run_invoke()` to misbehave in your named way.
4. `chmod +x` the new file.
5. Write a scenario in `scenarios/` that uses it. `setup.sh <mode>` picks
   up the new backend by filename — no edits to `setup.sh`/`teardown.sh`
   are needed.

### Negative-control protocol

The most common way integration tests lie is by passing for the wrong
reason. To defend:

1. Every scenario cites a specific pre-fix commit hash in its header.
2. Before trusting a green scenario, check out that hash and re-run. If
   it still passes, the assertion is too weak — fix it before merging.

Example (using the #63 grandchild scenarios, where `5c3385b2` is the
commit immediately before `2d451815`, the #63 fix):

```sh
git checkout 5c3385b2                                          # pre-#63
bash test/playground/scenarios/watchdog_kills_grandchild.sh
# must FAIL; if it passes, the scenario is not actually testing #63
git checkout -                                                 # back to HEAD
```

### `LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS`

Scenarios that need to trip the daemon's output-timeout watchdog quickly
export this env var before launching `loom daemon`. Read by
`Supervisor.GetOutputTimeout` (`internal/cli/daemon/supervisor/restart.go`);
the env var wins over fleet-db config because the wire schema does not
currently persist this field. Pure testability — no production behavior
change. The harness sets it to 15 seconds (vs. the 900s default).
