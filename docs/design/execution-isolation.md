# Execution isolation: what Loom actually isolates

Loom ships several isolation-shaped features — a container sandbox, a trust
level, a `read_only` role knob, tool allow/deny lists — and it is easy to read
them as one boundary. They are not. This note states the execution model
plainly so an operator can tell which knob bounds what, and which ones bound
nothing at the OS level.

The short version: Loom runs code at three levels, and **only the outermost
level can be containerized**. The process that runs the LLM and edits the
repository is not containerized on any local path.

## The three levels

| Level | What runs there | Containerizable | Mechanism |
| --- | --- | --- | --- |
| L1 — DriverRun bundle | The Flue workflow bundle: the orchestration program that decides what tasks exist | Yes | `LOOM_DRIVER_SANDBOX=container` (`internal/driver/sandbox/container.go`) |
| L2 — TaskRun leaf | The process that invokes the backend CLI and edits code | No | Host bridge spawns `node` directly (`internal/driver/task_bridge.go`) |
| L3 — daemon agent | The `loom <role> --daemon-mode` worker the supervisor spawns | No | Plain host process; the backend CLI's own sandbox is switched off |

Daytona is the exception that cuts across L2/L3 and is covered below: it is the
only mechanism in the tree that puts an agent leaf somewhere other than the
host.

## L1 — the DriverRun bundle

This is the one real local sandbox. `ResolveSandboxLauncher`
(`internal/driver/sandbox/container.go`) reads `LOOM_DRIVER_SANDBOX`: `process`
(the default) keeps the local node-process launcher, `container` returns the
rootless container launcher, and any other value is an error. Serve wires it in
`buildDriverExecutor` (`internal/cli/serve/serve.go`), which disables the
executor outright on an invalid sandbox configuration rather than degrading to
a host process.

What container mode actually gives you, per `internal/driver/sandbox/container.go`:

- Rootless podman, falling back to docker when podman is not on `PATH`;
  override with `LOOM_DRIVER_SANDBOX_BINARY`.
- Image `docker.io/library/node:22-slim`, override with
  `LOOM_DRIVER_SANDBOX_IMAGE`.
- An optional hardened OCI runtime: `LOOM_DRIVER_SANDBOX_RUNTIME=runsc` for
  gVisor; empty uses the engine default.
- Mandatory resource caps — `LOOM_DRIVER_SANDBOX_MEMORY` (1g),
  `LOOM_DRIVER_SANDBOX_CPUS` (1.0), `LOOM_DRIVER_SANDBOX_PIDS_LIMIT` (256). The
  zero config is capped, not uncapped.
- Host filesystem unreachable except the bundle and launcher identity mounts;
  runtime env is exactly the launch spec's env, delivered through an ephemeral
  `0600` env-file rather than argv.

Egress is bounded separately by `LOOM_DRIVER_SANDBOX_EGRESS`
(`internal/driver/sandbox/egress.go`): `all`, `serve-only`, `none`, or
`delegated`. Empty resolves per run trust level — trusted gets `all`, untrusted
gets `serve-only`, which is `--network=none` plus a unix-socket relay that can
reach the serve driver API and nothing else.

Trust placement is enforced at this level regardless of mode.
`RefuseUntrustedPlacement` (`internal/driver/sandbox/policy.go`) fails the run
with `errorClass=sandbox_required` when an untrusted driver resolves to a
launcher that does not implement `IsolatingLauncher`. Nothing is spawned; there
is no silent fallback.

**What this does not cover.** L1 bounds the workflow bundle — the program that
schedules work. It does not bound the agent. A DriverRun executing inside a
gVisor container can still produce TaskRuns whose leaves run as host processes.

## L2 — the TaskRun leaf

`HostBridgeTaskExecutor` (`internal/driver/task_bridge.go`) has no sandbox
launcher seam at all — compare its struct fields with `Executor.SandboxLauncher`
in `internal/driver/executor.go`. It spawns the runner with a plain
`exec.CommandContext(ctx, "node", launcherPath)` in the host worktree. The same
is true of `RunBundledTaskRunner` (`internal/driver/bundled_runner.go`), the
one-shot path the daemon execution leaf uses.

The code says so directly. Both trust gates in
`internal/driver/task_bridge_session.go` — `refuseUntrustedTaskRunnerPreflight`
and `refuseUntrustedTaskRunnerExecution` — carry the same message:

> child runner `%q` is untrusted and the host bridge does not isolate runner
> code

That sentence is the whole L2 story: the bridge's answer to untrusted code is
refusal, because it has no sandbox to put it in.

Practical consequence: setting `LOOM_DRIVER_SANDBOX=container` containerizes
the orchestration program and leaves the process that runs your model and
writes your files on the host, with the host's credentials and the host's
filesystem.

## L3 — daemon agents

Daemon-supervised agents are never sandboxed. `buildAgentExecCmd`
(`internal/cli/daemon/supervisor/spawn.go`) builds a plain
`exec.Command(loomPath, "<role>", "<worktree>", "--auto", "--daemon-mode")`;
the only process attributes set are the worktree as `cmd.Dir`, `Setpgid` for
process-group management, and `cli.FilteredEnv()`. None of those is isolation.

There is no container branch on that path. `ResolveSandboxLauncher` has exactly
one production caller — `buildDriverExecutor` in `internal/cli/serve/serve.go`
— and nothing under `internal/cli/daemon/` imports `internal/driver/sandbox` or
references a `SandboxLauncher`.

On top of that, the backend CLIs are launched with their own built-in sandboxes
explicitly disabled:

- **claude** — `--dangerously-skip-permissions`
  (`internal/cli/backends/backend_claude.go`, both the interactive and the
  resume/RunTurn argv builders; also
  `internal/cli/backends/harness_lead_runtime.go`).
- **codex** — `--dangerously-bypass-approvals-and-sandbox`, the non-`read_only`
  branch of `codexSandboxArgs` (`internal/cli/backends/backend_safety.go`),
  applied in `internal/cli/backends/backend_codex.go` and
  `internal/leadcontrol/codex_runtime.go`.
- **gemini** — `--approval-mode=yolo`, the non-`read_only` branch of
  `geminiApprovalModeArg` (`internal/cli/backends/backend_safety.go`), applied
  in `internal/cli/backends/backend_gemini.go`.
- **cursor** — `--force`, cursor-agent's permission bypass, applied
  unconditionally at every invocation site in
  `internal/cli/backends/backend_cursor.go`. There is no `read_only` branch;
  cursor is not in `SupportsHardReadOnly`, so a `read_only` cursor role gets
  the prompt preamble *and* `--force`.

There is also an `IS_SANDBOX=1` export in `buildClaudeEnv`
(`internal/cli/backends/backend_claude.go`) whose comment reads:

> claude-code refuses `--dangerously-skip-permissions` when running as root
> unless IS_SANDBOX is set. loom runs claude as root inside its isolated
> lead/agent container, so set it explicitly (FilteredEnv strips it otherwise).
> Harmless outside a container.

The premise in the middle of that comment does not hold on the local paths
Loom actually runs: there is no lead/agent container, because nothing in the
tree creates one for an agent. `IS_SANDBOX=1` there is a compatibility shim
that tells the backend CLI a sandbox exists so it will drop its own guardrails.

## Remote isolation: Daytona

Daytona is the only mechanism that actually relocates an agent leaf off the
host, and it is reachable from both planes.

- **Driver plane** — run the task under the named runner
  `daytona-task-runner` (`internal/driver/bundled_runner.go`,
  `internal/workflows/builtin/daytona-task-runner.ts`). The epic-runner payload
  the web UI builds can select it
  (`internal/webui/frontend/src/utils/epicRunnerPayload.ts`).
- **Daemon plane** — set `LOOM_DAEMON_LEAF=ts` to move the daemon's execution
  leaf onto the bundled TypeScript task runner, then
  `LOOM_DAEMON_LEAF_RUNNER=daytona-task-runner` to switch the entrypoint
  (`internal/cli/agent/tsruntime/tsruntime.go`). The Go supervisor is unchanged;
  it still spawns `loom <role> --daemon-mode` as a host process, and only the
  leaf's internal execution moves.

Two scoping facts about the daemon plane, both easy to trip over:

- `LOOM_DAEMON_LEAF=ts` reaches only the **built-in `plan` and `task` roles**.
  `tsruntime.Invoker` is called from `runPlanDaemon` and `runTaskDaemon`
  (`internal/cli/agent/plan.go`, `internal/cli/agent/task.go`), and
  `supervisor.BuiltInRoles` (`internal/cli/daemon/supervisor/types.go`) is
  exactly `{plan, task}`. Custom roles run `runAgentDaemon`
  (`internal/cli/agent/agent_cmd.go`), which calls
  `cli.InvokeAgentNonInteractive` directly and never touches the TS leaf, so
  they cannot be moved to Daytona this way.
- `LOOM_DAEMON_LEAF=ts` alone changes nothing about isolation. Its default
  entrypoint is `local-task-runner`, which is still a host process. The
  isolation comes from the runner switch, not the leaf switch.

Requirements, both paths:

- `DAYTONA_API_KEY`. `internal/cli/envfilter/envfilter.go` allowlists the
  `DAYTONA_` prefix into spawned agent environments for exactly this reason.
- **A network-reachable git URL.** The sandbox clones the repo; it cannot see
  your disk. `daytonaRepoURL` (`internal/cli/agent/tsruntime/tsruntime.go`)
  prefers an explicit `DAYTONA_REPO_URL` and otherwise falls back to the
  worktree's `origin` remote, skipping anything that looks like a local path or
  a `file:` URL. With neither, the runner fails
  `daytona_repo_url_missing` (`internal/workflows/builtin/daytona-task-runner.ts`).

So: **when a workspace's repos are network-reachable, the Daytona leaf is the
interim remote-isolation story for daemon agents.** When they are not, there is
no isolated option and the agent runs on the host.

Daytona delivers its result through its own PR/sandbox path rather than a
top-level patch, so the daemon leaf's patch-back step is a no-op for that
entrypoint (`internal/cli/agent/tsruntime/tsruntime.go`).

### Give sandboxed runs room: `LOOM_DRIVER_STALE_TASK_MAX_AGE`

Sandboxed runs are slow. A Daytona provision plus clone plus agent run is
routinely 10-15 minutes, which is long enough to trip a stale-heartbeat
sweeper sized for local tasks.

`defaultStaleTaskRunMaxAge` in `internal/driver/stale_task_sweeper.go` is
**20 minutes**, chosen for exactly that shape after a real Daytona run was
observed swept at 11.3 minutes under the older 5-minute value. Operators can
tighten or loosen it with `LOOM_DRIVER_STALE_TASK_MAX_AGE`, read in seconds and
clamped to 1..86400 (`driverStaleTaskMaxAge` in
`internal/cli/serve/serve_loops.go`, `boundedIntEnv` in
`internal/cli/serve/serve.go`).

Leave it unset unless you have a reason. `driverStaleTaskMaxAge` now returns 0
when the variable is empty, which defers to the package default. It previously
returned a non-zero serve-side default; because `StaleTaskSweeper.MaxAge > 0`
always wins, that value shadowed the 20-minute constant entirely and re-created
the live-run sweep the constant was raised to prevent. Both files carry the
note.

## What is NOT isolation

Three features are routinely mistaken for a security boundary. None of them is
one.

### `read_only` and the tool lists

`internal/cli/backends/backend_safety.go` is the authority here. The role knobs
`allowed_tools`, `denied_tools`, and `read_only` arrive as
`LOOM_ALLOWED_TOOLS` / `LOOM_DENIED_TOOLS` / `LOOM_READ_ONLY` and are turned
into real backend CLI flags where a backend has them. The governing rule is
that a knob never silently means less than it says.

Per backend:

- **claude** — `--allowedTools` / `--disallowedTools`. `read_only` expands to
  denying `Write,Edit,NotebookEdit,Bash`. `Bash` is in that set deliberately: a
  shell can write through redirection, so leaving it would make "read-only" a
  lie. Roles that need a read-only shell should spell out `denied_tools`
  instead.
- **codex** — `read_only` selects `--sandbox read-only`, an OS-level policy,
  *instead of* `--dangerously-bypass-approvals-and-sandbox`. No tool
  vocabulary.
- **gemini** — `read_only` selects `--approval-mode=plan` instead of
  `--approval-mode=yolo`. No supported tool lists; upstream deprecated
  `--allowed-tools`.
- **everything else** — opencode, cursor, external, and the deterministic test
  backend have no hard mechanism.

The two knob families are then treated differently by `ValidateSafetyKnobs`:

- **Tool lists fail closed.** A backend with no tool-control flags refuses the
  run. `SupportsToolControl` is true for `claude` only. The daemon parks the
  agent with a `SpawnFailure`-class error and picks up a corrected role on the
  next config poll (`internal/cli/daemon/supervisor/backend.go`). There is no
  soft equivalent of an allowlist, so an unapplied one would be pure fiction.
- **`read_only` degrades, loudly.** `SupportsHardReadOnly` is true for
  `claude`, `codex`, and `gemini`. On any other backend `read_only` falls back
  to a prompt preamble (`ReadOnlyPreamble`, `internal/cli/agent/prompts.go`)
  and the supervisor logs
  `SOFT ENFORCEMENT ONLY — backend %q has no hard read-only mechanism: read_only is enforced by prompt preamble only, so the agent CAN still write`
  once per distinct message. Failing closed here was tried and broke the
  product: `seedBuiltInRoles` gives the built-in `plan` role `ReadOnly: true`
  on every workspace, so a hard refusal refuses every planner on every backend
  without a hard mechanism.

Read that as written. On a soft backend, `read_only` is an instruction the
model is asked to follow. **An agent under `read_only` on a soft backend can
still write.** Even on a hard backend, this is process-level tool and approval
policy — a meaningful restriction on what the agent is equipped to do, not a
boundary around what the process can reach.

One gap to know about: the controlled-lead runtime builds its argv
independently. `harnessLeadInvocation`
(`internal/cli/backends/harness_lead_runtime.go`) hardcodes each backend's
permissive flags — `--dangerously-skip-permissions`, `--approval-mode=yolo`,
`--force` — and never calls `appendClaudeSafetyArgs`, `geminiApprovalModeArg`,
or `codexSandboxArgs`. So an interactive role carrying `read_only` or tool
lists gets them applied on the daemon/agent invoker paths but **not** when it
launches through `RunControlledLeadRuntime`.

### The `untrusted` trust level

`untrusted` is a refusal, not a sandbox. It causes Loom to decline to run
something; it never confines it. See the next section for the shape that
refusal takes on named runners.

### Per-run git worktrees

Isolated per-run worktrees (`internal/driver/task_worktree_resolver.go`) keep
concurrent runs from stepping on each other's files. They are a concurrency
mechanism. The process still has the whole filesystem.

## Consequence: untrusted named runners can never execute

`loom workflow build` stamps every version it registers
`domain.DriverTrustUntrusted` (`internal/cli/workflow/workflow_cmd.go`) — no
self-elevation from a local build.

At L2, both trust gates in `internal/driver/task_bridge_session.go` fail closed
with no launcher escape:

- `refuseUntrustedTaskRunnerPreflight` errors out of
  `PreflightTaskProvider`.
- `refuseUntrustedTaskRunnerExecution` returns a terminal `sandbox_required`
  failure from `ExecuteTask`.

Neither consults the resolved launcher, unlike `RefuseUntrustedPlacement` at
L1, which passes when the launcher isolates. So for a named task runner,
container mode does **not** unblock untrusted code — only trust does. A custom
workflow with a named runner is inert until an operator approves it:
`DriverVersionEffectiveTrust` (`internal/driver/approval.go`) returns `trusted`
for an approved version, and `loom workflow approve <id> --version <v>` is what
sets that. Builtin bundles (`internal/workflows/workflows.go`) and
operator/CLI registration (`internal/driver/register.go`,
`internal/cli/driver/driver_cmd.go`) stamp trusted at registration.

## Designed, not built

`docs/product/container-runner-mvp-spec.md` specifies containerized *agent*
runners — Podman-hosted planners and coders with run metadata, streamed logs,
and preserved artifacts. It is marked **Draft** and it is not implemented:
outside `internal/driver/sandbox/`, no production Go code in the tree invokes a
container engine. Read it as the intended design for closing the L2/L3 gap, not
as a description of current behavior.

## Related

- `docs/security.md` — credential storage, API surface hardening, workspace
  clone SSRF bounds.
- `docs/product/container-runner-mvp-spec.md` — the designed containerized
  agent runner.
- `AGENTS.md` — deploy-facing knob reference for the L1 workflow sandbox,
  including the SELinux and macOS caveats.
