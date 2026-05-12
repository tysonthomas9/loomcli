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

`setup.sh` is **not** idempotent across orphan fleet-db state — if a previous
run left stale role records (e.g. you `kill -9`'d the daemon mid-create),
re-running may fail with `create role "plan": HTTP 409 already exists`. Fix
by stopping `loom serve`, removing `~/.loom`, and starting fresh. Normal
`./teardown.sh && ./setup.sh` works when the previous run completed.

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
