# Playground

Daemon-lifecycle **failure-mode harness** for the loom supervisor: deterministic
mock backends that crash, hang, or run slow on demand, plus scenario scripts
that assert the supervisor classifies, kills, sweeps, and times them out
correctly. Covers code paths the Go unit tests in
`internal/cli/daemon/supervisor/**` structurally cannot — real pgroups, real
orphan PIDs, real watchdog timing, real fleet-db lock cleanup.

A happy-path mock backend (`loom-backend-playground`) and a workspace
scaffold come along for the ride; see [§ Happy-path scaffold](#happy-path-scaffold)
below. For general full-stack dogfooding (Docker, distributed mode, web UI)
use [`test/local-mode/`](../local-mode/) instead — this directory is not the
right tool for that.

`setup.sh` creates a throwaway git repo, registers it as a loom workspace,
defines `plan` + `task` agents that dispatch to one of the
`loom-backend-playground[-mode]` scripts, and seeds a few sample tasks. From
there scenarios drive the daemon and assert against its behavior; humans can
also drive it manually with `loom monitor` / `loom lead` / the web UI.

## Prereqs

- `loom` on `PATH`.
- `loom serve` already running on `http://localhost:8080` (or the Loom Desktop
  app), so the daemon spawned by `loom workspace ops ensure-runtime` has a
  backend to talk to.

## Happy-path scaffold

The happy-path mock (`loom-backend-playground`) and its 3 seed tasks exist
as scaffolding for the failure-mode scenarios — every scenario reuses the
same `setup.sh`/`teardown.sh` plumbing. It is *not* a substitute for the
real `loom lead` approval flow; the planner role skips that step on purpose
so scenarios can get to `open → closed` deterministically without human
input. If you want to exercise the real lead/plan/task UX, run
`test/local-mode/` or a real workspace.

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
  (with the design attached — skipping the human `loom lead` approval step
  on purpose so failure-mode scenarios can reach `open → closed` without
  human input; see [§ Happy-path scaffold](#happy-path-scaffold)),
  call `loom complete`.
- `LOOM_ROLE=task` → claim task, append to `playground.txt`, git commit,
  close task, call `loom complete`.

Other env vars: `LOOM_ASSIGNED_TASK_ID`, `LOOM_AGENT_NAME`,
`LOOM_SESSION_ID`, `LOOM_WORKSPACE_RUNTIME_DIR`. Override the per-step delay
with `LOOM_PLAYGROUND_STEP_DELAY` (default 1 second) when you want the
monitor UI to keep up visually.

## Comparison to `test/local-mode/`

Different jobs — pick by purpose, not by overlap:

- **`test/local-mode/`** — full Docker/Podman stack (fleet-db, daemon, web UI)
  for distributed-mode dogfooding and full-stack regression. Heavy; slow to
  start; correct choice for "does the whole product still work".
- **`test/playground/` (here)** — single-host failure-mode harness for the
  daemon supervisor. Assumes `loom serve` is already running. Cheap; fast;
  deterministic. Correct choice for "does the supervisor still classify a
  crashed/hung/slow backend correctly".

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

The core of the playground. While `loom-backend-playground` exercises the
happy-path scaffold, the sibling backends below misbehave in named,
reproducible ways so scenarios can verify the daemon's watchdog, orphan
sweep, retry/backoff, and classification paths — without LLM calls and
without the cost of bringing up Docker.

### Failure-mode backend zoo

| Backend                                | Failure mode                                                                                                          | Tests                                                                                              |
| -------------------------------------- | --------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `loom-backend-playground-hang`         | Parent goes silent. No descendants.                                                                                   | Basic watchdog (single-pgroup case). Simplest template for "copy this when adding a new bug."      |
| `loom-backend-playground-crash`        | Exits non-zero a few seconds after invoke.                                                                            | Failure classification + retry/backoff.                                                            |
| `loom-backend-playground-slow`         | Writes one stdout line every `interval` seconds (less than the watchdog timeout).                                     | Regression guard: legitimate slow work must NOT trigger the watchdog.                              |

Each backend implements the same `meta`/`health`/`invoke` contract as
`loom-backend-playground` and self-documents its failure mode in a header
comment.

### Running scenarios

Scenarios live in `scenarios_test.go` as Go tests under the `playground`
build tag. They share helpers (`startScenarioDaemon`, `waitForFile`,
`waitForLogLine`, `runLoom`, `scenarioCleanup`) and call `setup.sh` /
`teardown.sh` for workspace orchestration.

```sh
# Run all scenarios (happy path + failure modes)
go test -tags=playground -v ./test/playground/...

# Run one
go test -tags=playground -v -run TestPlaygroundSlowBackendNotKilled ./test/playground/...
```

`requireServe` skips the test if `loom serve` isn't reachable, so the
suite is safe to run on machines that aren't set up.

### Writing a new scenario

Add a new `TestPlayground<Mode>` function to `scenarios_test.go`. The shape
is:

```go
func TestPlaygroundMyMode(t *testing.T) {
    requireServe(t)
    const scenario = "mymode" // picks loom-backend-playground-mymode

    _ = exec.Command("bash",
        filepath.Join(hereDir(t), "teardown.sh"), scenario).Run()

    runScenarioScript(t, "setup.sh", []string{scenario}, nil)

    daemonLog := filepath.Join(scenarioRuntimeDir(t, scenario), scenario+".daemon.log")
    daemon := startScenarioDaemon(t, scenario, daemonLog)
    t.Cleanup(func() { scenarioCleanup(t, scenario, daemon) })

    runLoom(t, scenario, "data", "create", "--title", "Probe",
        "--type", "task", "--priority", "2",
        "--status", "open", "--design", "...")

    // assert: waitForFile, waitForLogLine, logHasLine, t.Errorf on miss
}
```

The test docstring **must** document:

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
5. Write a `TestPlayground<Mode>` test in `scenarios_test.go` that uses
   it. `setup.sh <mode>` picks up the new backend by filename — no edits
   to `setup.sh`/`teardown.sh` are needed.

### Negative-control protocol

The most common way integration tests lie is by passing for the wrong
reason. To defend:

1. Every scenario docstring cites a specific pre-fix commit hash.
2. Before trusting a green scenario, check out that hash and re-run. If
   it still passes, the assertion is too weak — fix it before merging.

Example (replace `<pre-fix-commit>` with the hash from your scenario
docstring):

```sh
git checkout <pre-fix-commit>
go test -tags=playground -v -run TestPlaygroundMyMode ./test/playground/...
# must FAIL; if it passes, the scenario is not actually testing the bug
git checkout -                                                 # back to HEAD
```

### `LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS`

Scenarios that need to trip the daemon's output-timeout watchdog quickly
export this env var before launching `loom daemon`. Read by
`Supervisor.GetOutputTimeout` (`internal/cli/daemon/supervisor/restart.go`);
the env var wins over fleet-db config because the wire schema does not
currently persist this field. Pure testability — no production behavior
change. The harness sets it to 15 seconds (vs. the 900s default).
