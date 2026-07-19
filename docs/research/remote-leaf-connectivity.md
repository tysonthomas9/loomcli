# Remote-leaf connectivity and auth surface (LOOMCLI-91 research)

Question: can daytona/remote-placed flue leaves call loom **driver ops** at agent
spawn time, or do they need post-hoc session materialization at TaskRun finish?

Read-only code survey of `internal/driver/`, `internal/workflows/builtin/`,
`internal/webui/handlers/driverapi/` + `taskrunapi/`, `sdk/`, `internal/cli/serve/`,
`e2e/`, `test/local-mode/`. No stacks were started; everything below is from code,
with runtime-verification gaps listed at the end.

## Facts

### 1. "Daytona placement" means daytona *sandbox*, host-side *runner*

- `TaskExecRequest` carries two placements: `RunnerPlacement` (where the leaf
  process runs) and `SandboxPlacement` (where agent tool execution happens) —
  `internal/driver/task_request.go:100-101` (options struct: `task_request.go:52-53`).
- The `flue-daytona` provider profile resolves to `RunnerPlacement.Provider="flue"`
  and `SandboxPlacement.Provider="daytona"` — `internal/driver/task_scheduling.go:208-219`.
- There is **no code path that launches the leaf process itself inside Daytona**.
  The leaf is always a `node` child process of a loom-serve node:
  - synchronous spawn via the `exec-task` driver op inside the serve process —
    `internal/webui/handlers/driverapi/module.go:575-608`;
  - or enqueue (`EnqueueOnly`) + claim by serve's `TaskWorker` loops —
    `internal/cli/serve/serve.go:314-344`, `internal/driver/task_worker.go:48-124`
    (worker stamps `RunnerPlacement.Provider="loom-serve"` by default,
    `task_worker.go:171-183`).
- The host bridge launches flue leaves as `node <temp launcher>` which forks the
  bundle server in one-shot IPC mode — `internal/driver/task_bridge.go:355-401`
  (launcher source `task_bridge.go:456-659`, `FLUE_INTERNAL_CLI_IPC` at :596).
- `daytona-task-runner.ts` runs on that host and *itself* creates the Daytona
  sandbox via `@daytona/sdk` (`internal/workflows/builtin/daytona-task-runner.ts:136-147`),
  then drives it over the Daytona REST API (`DaytonaSandboxApi`, :373-434).
  Model calls (openai-codex) are made from the **host** process with host-side
  codex auth files (:436-509); only file/shell operations execute in the sandbox.
- Bundle header comment states the same: "provisions a Daytona sandbox … host-side
  harness driving the sandbox" — `internal/driver/bundled_runner.go:16-20`.

### 2. Env/URLs a leaf receives at spawn

`HostBridgeTaskExecutor.taskRunnerEnv` — `internal/driver/task_bridge.go:661-709`:

- `LOOM_TASK_RUN_REQUEST_JSON` (full `TaskExecRequest`, incl. `lease_token`),
  `LOOM_DRIVER_WORKSPACE`, `LOOM_DRIVER_RUN_ID` (parent DriverRun id),
  `LOOM_DRIVER_STEP_ID`, `LOOM_TASK_RUN_ID`, `LOOM_TASK_ID`,
  the leaf's own lease tuple `LOOM_TASK_RUN_NODE_ID/LEASE_ID/LEASE_TOKEN/FENCING_TOKEN`,
  placement JSON `LOOM_TASK_RUN_RUNNER_PLACEMENT_JSON` / `LOOM_TASK_RUN_SANDBOX_PLACEMENT_JSON`
  (:684-685).
- `LOOM_TASK_RUN_API_URL` — only when the executor's `APIBaseURL` is set
  (:687-689). This is the **task-run API** (taskrunapi), not the driver-op API.
- NOT exported to leaves: `LOOM_DRIVER_API_URL`, `LOOM_RUN_TOKEN`, the parent's
  lease id / fencing token. The base env is a default-deny allowlist
  (`internal/driver/env.go:5-31`, `env.go:125-151`); any inherited `*TOKEN*`
  var is stripped (`env.go:67-74`), and `LOOM_DRIVER_API_URL` is simply not on
  the allowlist, so a leaf cannot even inherit it from the serve process.
- Launcher gate: the leaf refuses to run unless `LOOM_TASK_RUN_LEASE_TOKEN`
  equals the request's `lease_token` — `task_bridge.go:569-571`.
- Env widening (provider API keys, `GITHUB_TOKEN`, `CODEX_HOME`, …) is strictly
  scoped to the `local-task-runner` entrypoint — `env.go:82-100`,
  `internal/driver/task_bridge_artifacts.go:290-298`. Daytona/remote entrypoints
  keep the strict filter.

### 3. What URL `LOOM_TASK_RUN_API_URL` actually is

- Serve computes it via `driverAPIBaseURL()`: **loopback by default**
  (`http://127.0.0.1:<port>`), overridable with `LOOM_DRIVER_API_URL` on the
  serve process — `internal/cli/serve/serve.go:409-426`. Comment: "Driver
  runtimes are local children of the executor, so loopback is always reachable."
- Same value feeds both the driverapi module config and the TaskWorker
  (`serve.go:321`, `serve.go:372`, `driverapi/module.go:66-69` → passed to
  `HostBridgeTaskExecutor.APIBaseURL` at `driverapi/module.go:588-595`).
- Serve binds `127.0.0.1` by default (`serve.go:113-114`, `serve.go:148-156`);
  non-local binds only print a warning (`serve.go:565-569`). So both API
  surfaces are, by default, reachable **only from the serve node itself**.

### 4. Driver ops (driverapi) — surface and auth

- Op map (claim-ready, epic-get/snapshot, list-agents, deliver-*, exec-task,
  task-run-get, active-task-runs, recover-stale-tasks, complete-task,
  release-task, connector-dispatch, emit-event, eval ops) —
  `internal/webui/handlers/driverapi/module.go:141-162`; routes
  `POST /api/workspaces/{ws}/driver/{op}` plus watch/await/workflows —
  `module.go:191-203`.
- Every op calls `verifyParent` → `VerifyRunningDriverRun` —
  `module.go:283-285`, `internal/driver/run.go:149-178`: the parent DriverRun
  must be `running`, and when it holds a lease (all executor-claimed runs do),
  the caller must present matching `nodeID` + `leaseID` + positive fencing
  token, proven by a **fenced heartbeat**. `X-Loom-Driver-Run-Id` alone is
  `ErrNotOwner`.
- Credential paths (`driverapi/token_auth.go:34-53`):
  1. run-scoped HS256 bearer (`LOOM_RUN_TOKEN`), minted at DriverRun claim
     (`internal/driver/executor.go:185-201`; TTL default 24h,
     `run.go:210-218`) and injected **only** into the parent workflow
     runtime env (`executor.go:826-829`, exported alongside
     `LOOM_DRIVER_API_URL` at `executor.go:790-798`);
  2. legacy header quad (`X-Loom-Driver-Run-Id/-Node-Id/-Lease-Id/-Fencing-Token`)
     plus optional shared static `LOOM_DRIVER_API_TOKEN` (`serve.go:428-432`).
- A TaskRun leaf holds `LOOM_DRIVER_RUN_ID` (env) but not the parent's
  node/lease/fence, and never a run token → it cannot pass `verifyParent`
  even though the endpoint is network-reachable from the host-placed leaf.

### 5. Task-run ops (taskrunapi) — what leaves CAN call

- `POST /api/workspaces/{ws}/task-run/{op}` with ops: get, task-get, heartbeat,
  log-append, complete, runtime-credential, artifact-declare/get/list/finalize,
  plus raw `PUT …/task-run/artifacts/{id}/content` —
  `internal/webui/handlers/taskrunapi/module.go:105-116`, `module.go:141-149`.
- Auth: `Authorization: Bearer <lease token>` + `X-Loom-Task-Run-*` identity
  headers, verified by passing the tuple through the store's fenced task-run
  checks (a fenced no-op heartbeat for non-mutating ops) —
  `taskrunapi/module.go:12-28`. Purpose per package doc: task harnesses
  "never hold fleet-db credentials" (§9.1).
- The SDK client is transport-switching: `LOOM_TASK_RUN_API_URL` set → serve
  mode, lease token is the ONLY credential (`sdk/runner.js:53-61`, headers
  :411-419); unset → legacy direct-fleet-db with `LOOM_FLEET_DB_URL` +
  `LOOM_FLEET_DB_API_KEY` (:421-433). `TaskRunClient.fromEnv`: `sdk/runner.js:34-51`.
- Both leaf runners use it from spawn time:
  - daytona-task-runner: `loadTaskContext` → `TaskRunClient.fromEnv()` +
    `getTask()` (`daytona-task-runner.ts:693-704`),
    `runtimeCredentials.get({provider: "daytona"|"github"})` (:554-577, served
    by `taskrunapi/credentials.go:12-50` from sealed local settings), and
    mid-run artifact declare/upload/finalize (:706-790).
  - local-task-runner: `loadTask`/`requestPayload` via `TaskRunClient.fromEnv()`
    (`internal/workflows/builtin/local-task-runner.ts:1633-1645`, :1653-1657).

### 6. Inside the Daytona sandbox: no loom connectivity, no loom credentials

- The sandbox runs only shell/file operations shipped over the Daytona API from
  the host harness. Nothing in the sandbox dials loom; the loopback
  `LOOM_TASK_RUN_API_URL` would be meaningless there anyway.
- A leak probe run *inside* the sandbox asserts none of the sensitive env names
  (incl. `LOOM_TASK_RUN_LEASE_TOKEN`, `DAYTONA_API_KEY`, provider keys) are
  present, failing the run otherwise — `daytona-task-runner.ts:184-188` and
  `sandboxLeakProbeCommand` :1056-1080 (mirrors `env.go`
  `trustedLocalProviderCredentials`).
- The GitHub token does transit into sandbox **shell command argv** for
  clone/push (`gitWithGitHubAuth`, `daytona-task-runner.ts:956-962`, used at
  :161, :836-840); the PR REST calls are made from the host (:891-911).
- Repo access from the sandbox is outbound-only public network: "DAYTONA_REPO_URL
  must be a network-reachable git URL the cloud sandbox can clone (the
  local-mode fixture repo is a local file remote and won't work)" —
  `test/local-mode/docker-compose.daytona.yml:11-13`.

### 7. Session materialization is already host-side, around the leaf

- At spawn: `startFlueTaskSession` creates AgentSession `flue-<taskRunID>`
  (status running, kind task) via the serve-side store and starts a 30s
  heartbeat goroutine — `internal/driver/task_bridge_session.go:157-197`,
  :199-214. The leaf never touches session APIs.
- At finish: `finishFlueTaskSession` finalizes status/exit/summary/metadata from
  the runner's returned result — `task_bridge_session.go:216-262`; transcript,
  logs, and patch evidence come back **in the leaf's stdout result JSON** and
  are persisted post-hoc by the bridge (`task_bridge.go:284-301`,
  `persistRunnerOutputArtifacts` in `task_bridge_artifacts.go:119`+,
  patch artifact + patch-back `task_bridge.go:825-905`).
- Pre-persist validation gate: an empty/non-terminal runner result fails closed
  (`invalid_task_result`) and never stamps evidence — `task_bridge.go:266-274`,
  `task_bridge_session.go:18-33`.

### 8. Adjacent paths (for completeness)

- Daemon TS leaf (`LOOM_DAEMON_LEAF=ts`, `LOOM_DAEMON_LEAF_RUNNER=daytona-task-runner`):
  runs the same bundle in-place via `RunBundledTaskRunner`
  (`internal/cli/agent/tsruntime/tsruntime.go:93-156`,
  `internal/driver/bundled_runner.go:56-133`). Env is the daemon's filtered host
  env passed through (`bundled_runner.go:105-119`); the compose override notes
  "runtime credential + artifact APIs are reached via LOOM_FLEET_DB_URL"
  (`test/local-mode/docker-compose.daytona.yml:1-13`) — i.e. the legacy
  direct-fleet-db SDK transport, and when no artifact client exists the patch
  is returned inline instead (`daytona-task-runner.ts:706-717`).
- Workflow (parent DriverRun) sandboxing: `LOOM_DRIVER_SANDBOX=container` +
  egress modes; untrusted default `serve-only` = `--network=none` + unix-socket
  relay that rewrites `LOOM_DRIVER_API_URL` to `http://127.0.0.1:8484`
  in-container, host-side relay pinned to the serve address —
  `internal/driver/sandbox/egress.go:1-80`, `serve.go:352-381`. This applies to
  DriverRun bundles, not TaskRun leaves.
- e2e daytona runs are podman containers on the dev host with fleet-db mounted
  in-container; the Daytona SDK root is bind-mounted, outbound cloud access from
  the container — `e2e/run_epic_runner_real_codex_octocat_podman.sh:47-60`.

## Connectivity verdict per placement

**Local host-bridge leaf (`local-task-runner`, and any flue leaf process):**
YES at spawn time — the leaf is a child process on the serve node and
`LOOM_TASK_RUN_API_URL` is the serve loopback, so it can (and does) call the
**task-run ops** from its first instruction (`getTask`, heartbeat,
runtime-credential, artifact ops). It can *reach* the driver-op routes on the
same loopback but cannot *authorize* against them (see auth verdict).

**Daytona-placed leaf (`daytona-task-runner`):** the leaf process is still
host-placed, so the same YES applies to the harness side: it calls task-run ops
(task-get, runtime-credential for the Daytona/GitHub keys, artifact
declare/upload/finalize) at spawn and mid-run, over serve loopback. The
*sandbox* (where agent tool execution happens) has NO path back to loom: no URL
that resolves (loopback default), no credentials (leak-probe-enforced), and
loom's APIs are bound to the serve host's loopback by default. Everything the
in-sandbox agent produces is ferried back by the host harness over the Daytona
API and lands in loom either via the harness's task-run-API calls (artifacts)
or via the bridge's post-hoc persistence of the result JSON (transcript, logs,
session finalize).

**Driver ops specifically:** NO for every leaf, local or daytona, at any time —
not a network problem but a credential one. That is by design (§9.1/§9.5:
leaves get the lease-scoped task-run surface; only the parent workflow runtime
holds driver-op credentials).

**Post-hoc session materialization:** already the shipped model. The AgentSession
for a flue leaf is created host-side at spawn (running + heartbeats), and its
transcript/evidence are materialized at TaskRun finish from the runner result.
Nothing about daytona placement forces *more* post-hoc work than local placement
— the only truly post-hoc pieces are transcript/log/patch persistence, identical
for both placements.

## Auth verdict (what a leaf holds at spawn time)

At spawn a flue leaf holds exactly:

1. **Per-task-run lease token** (`LOOM_TASK_RUN_LEASE_TOKEN` + node/lease/fence
   identity env) — valid ONLY for the taskrunapi surface, verified by fenced
   store checks; lifetime bounded by the lease itself (superseded lease/terminal
   run rejects). Minted per-request (`task_scheduling.go:276-282` or the
   worker claim path).
2. **Parent DriverRun id as data** (`LOOM_DRIVER_RUN_ID`) — an identifier, not a
   credential; `verifyParent` requires the parent's lease quad or a run token,
   neither of which reaches leaf env (allowlist filter + explicit non-export).
3. `local-task-runner` only: host provider credentials
   (ANTHROPIC/OPENAI/GEMINI/etc. API keys, `GITHUB_TOKEN`, `CODEX_HOME`) via the
   trusted-local widening, plus sealed local-settings GitHub token/backends
   (`task_bridge.go:701-708`, :712-732).
4. `daytona-task-runner`: NO ambient provider creds; it fetches Daytona/GitHub
   credentials at runtime through the lease-authenticated `runtime-credential`
   op (or `*_CREDENTIAL_FILE` fallbacks) and reads codex OAuth from host disk.

Driver-op credentials (run token `LOOM_RUN_TOKEN`, static
`LOOM_DRIVER_API_TOKEN`, parent lease quad) exist only in the parent workflow
runtime env (`executor.go:781-838`) and never in a leaf. Fleet-db credentials
(`LOOM_FLEET_DB_URL/_API_KEY`) are on the sensitive blocklist for all bridge
leaves (`env.go:44-47`); only the daemon-leaf compose path still passes a
fleet-db URL through.

## Gaps / unknowns requiring runtime verification

1. **Non-loopback serve deployments.** `LOOM_DRIVER_API_URL` can be overridden
   (TLS front) and serve can bind non-loopback. Whether any real deployment does
   this — and thus whether a daytona sandbox *could* route to serve if a
   credential were smuggled in — is a deployment-config question, not answerable
   from code.
2. **Daemon-leaf credential path in practice.** The compose comment says the
   daemon TS leaf reaches credential/artifact APIs via `LOOM_FLEET_DB_URL`
   (legacy transport with deployment API key). Whether the daemon's
   `FilteredEnv` actually delivers those vars to the leaf in the current stack,
   and whether `runtime-credential` therefore falls back to
   `DAYTONA_CREDENTIAL_FILE`, needs a live local-mode-daytona run.
3. **Serve-mode `runtime-credential` on headless serve.** It unseals from
   `LocalSettingsDir` (desktop-local sealed credentials); behavior when serve
   runs without configured local settings (e.g. CI) needs runtime confirmation.
4. **Leak-probe drift.** The sandbox probe checks a fixed name list that must
   mirror `env.go`; only a live sandbox run proves nothing new leaks.
5. **Egress relay on macOS.** The serve-only unix-socket relay is documented as
   non-functional across the podman-machine VM boundary (`egress.go:29-33`) —
   Linux-only enforcement; runtime check required per host OS.
6. **Run-token TTL vs long runs.** Default 24h max run duration
   (`run.go:217-218`) bounds parent workflows (hence leaf spawning); behavior at
   expiry mid-epic (token_expired → resume path) is runtime behavior.
7. **Timing of first leaf → serve call under load.** The lease is written by
   `ClaimQueued` before the bridge spawns the leaf, so the lease token should
   verify immediately; racing heartbeat/claim ordering under fleet-db (vs
   memstore) is only provable live.
