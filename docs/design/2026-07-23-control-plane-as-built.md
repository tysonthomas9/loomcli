# Control Plane — As Built

> **Status:** Current · *audited 2026-08-03*

Where the distributed control plane actually lives in this repo. Written
because the design docs in this cluster describe either a pre-2026-05 past
(`distributed-control-plane.md`, `distributed-control-plane-data-model.md`)
or an unbuilt future (the V2 proposals), and none of them names
`internal/driver` — which is where node registration, leases, fencing,
task-run claim, and sandbox launch actually happen.

This is a map, not a design. Every claim below is a `path:line` you can
open. Nothing here is aspirational; if something is not implemented it is
not in this file.

Terminology: `docs/loom-glossary.md` forbids bare "node". In this document
every unqualified **node** is a **control-plane node** (`domain.Node`,
`internal/domain/control_plane.go:39`) — never a stack node and never
Node.js. Likewise every unqualified **task worker** is `driver.TaskWorker`
(`internal/driver/task_worker.go:18`), the serve-side claim loop, not a
worker role, a fleet-db Worker row, or the `loom worker` process.

## The two layers

"Control plane" in this repo means two things stacked. Always say which.

| Layer | What it is | Where |
|---|---|---|
| **fleet-db** | The control-plane *data service*. Holds workspaces, repos, roles, task runs, driver runs, nodes, leases, artifacts, terminal sessions. Loom talks to it over HTTP. | Client: `internal/backend/fleet`. Store contracts: `internal/store`. |
| **`loom serve`** | The control-plane *service* above fleet-db. Runs the driver executor, task-run workers, and the sweeper loops; holds the connector vault key; is what remote workers register with. | `internal/cli/serve/serve.go`, `internal/cli/serve/serve_loops.go`, `internal/driver`. |

`internal/connector/vault.go:26-27` states the boundary from the other
side: "Only the control plane (loomcli serve) ever reads it; stores hold
ciphertext only."

`loom worker` (`internal/cli/serve/worker/worker_cmd.go`) is the *client*
of that: a remote agent worker that connects to a `--control-plane` URL.

## Domain types

Fleet-db-backed records, all workspace-scoped.

| Type | File:line | Notes |
|---|---|---|
| `domain.Node` | `internal/domain/control_plane.go:39` | Machine or sandbox. `OwnerActor`, `Labels`, `Capabilities`, `ToolInventory`, `Capacity`, `DrainState`, `LastHeartbeat`, `ExpiresAt`. |
| `domain.RuntimeProvider` | `internal/domain/control_plane.go:21-29` | A **string enum** (`local`, `e2b`, `kubernetes`, `ci`, `other`), not an interface. Only `local` is used in practice. `e2b` is dead. |
| `domain.AgentSession` | `internal/domain/control_plane.go:81` | Observed agent session state. |
| `domain.TerminalSession` | `internal/domain/control_plane.go:112` | Global metadata for a node-local PTY. |
| `domain.Artifact` | `internal/domain/control_plane.go:134` | Durable run output. |
| `domain.AgentLease` | `internal/domain/control_plane.go:167` | Keyed by `SessionID`. `Token` + `FencingToken int64` + status. |
| `domain.AgentOwnershipLease` | `internal/domain/control_plane.go:182` | Keyed by `AgentID`. Same fencing shape. |
| `domain.WorkerProfile` | `internal/domain/platform.go:104` | Named execution profile: role, backend, repos, priority/parallel caps, capabilities, enabled. |
| `domain.AgentService` | `internal/domain/platform.go:147` | Long-lived service. Kinds at `platform.go:123-136`. |
| `domain.DriverRun` | driven via `internal/store/platform_store.go:507` | One workflow-bundle execution. |
| `domain.TaskRun` | `internal/domain/platform.go:498` | One finite task execution attempt. `LeaseID` + `FencingToken` inline at `:513-514`. |
| `domain.DaemonProfile` | `internal/domain/daemon_profile.go:13` | Per-workspace daemon config. Deliberately mixes machine-local `PIDFile`/`LogDir`/`EventsDir` into a fleet-db record; see its docstring at `:9-12`. |

There is **no** generic polymorphic `Lease` record and no `NodeHeartbeat`
record. Heartbeat is an operation; ownership is expressed by the two
purpose-built lease types plus the inline `LeaseID`/`FencingToken` pair on
runs.

## Store contracts

`internal/store/control_plane_store.go`:

| Interface | Line |
|---|---|
| `NodeStore` | 37 |
| `AgentSessionStore` | 95 |
| `TerminalSessionStore` | 146 |
| `ArtifactStore` | 225 |
| `AgentLeaseStore` | 258 |
| `AgentOwnershipLeaseStore` | 284 |
| `AgentCommandStore` | 316 |
| `AgentInboxMessageStore` | 366 |
| `WorkerStore` | 379 |

`internal/store/platform_store.go`:

| Interface | Line | Operations that matter here |
|---|---|---|
| `DriverRunStore` | 507 | `Create`, `Claim`, `Heartbeat`, `Finish`, `RecoverStale`, `RecoverStaleTaskRuns`, `Suspend` |
| `TaskRunStore` | 762 | `Create`, `ClaimQueued`, `Heartbeat`, `Requeue`, `Finish`, `Complete`, `AppendLog`, `ListLogs` |

`DriverRunStore.Heartbeat`/`Finish` are owner-fenced: they carry `nodeID`,
`leaseID`, and `fencingToken` (`Heartbeat` as parameters, `platform_store.go:513`;
`Finish` inside `DriverRunFinish`, `platform_store.go:456-459`) and reject a
caller that is not the current holder. `Claim` (`platform_store.go:512`) takes
`nodeID` + `leaseID` only — it is where the fencing token is minted, so it has
none to check.

## `internal/driver` — the execution loop

This is the package the older docs never mention. `loom serve` wires it up.

### Node registration and heartbeat

Two independent registrants, both writing `domain.Node` with
`RuntimeProvider = local`:

- Driver executor: `internal/driver/executor.go:444` `ensureNode` →
  `Nodes().Create` at `:452`; on `ErrAlreadyExists` it falls through to
  `Nodes().Heartbeat` at `:467`. Background heartbeat loop:
  `heartbeatExecutorNode`, `executor.go:559`.
- Task worker: `internal/driver/task_worker.go:192` `ensureNode` →
  `Nodes().Create` at `:200`, `Nodes().Heartbeat` at `:217`.
- The daemon supervisor runs its own node heartbeat goroutine:
  `internal/cli/daemon/supervisor/control_plane.go:47-49`.

Node TTL derives from the heartbeat interval (`executor.go:531`
`nodeTTL`).

### Driver runs (workflow bundles)

```text
queued DriverRun -> Claim(node, lease) -> verify bundle -> SandboxLauncher
                 -> Runner.Run -> Finish(fenced)   |  Suspend (awaiting event)
```

- `Executor.RunOnce`, `internal/driver/executor.go:115`.
- Next queued run: `:278` `nextQueuedRun` → `:318`
  `nextQueuedRunInWorkspace`.
- Run-scoped token minted at `:191` `mintRunToken`.
- Bundle integrity: `:611` `verifyBundleManifest`, with path containment at
  `:642` `safeBundleRoot` and `:657` `safeBundleFile`.
- Settle/finish, fenced: `:337` `settleClaimed`, `:356` `finish`.
- Stale recovery: `:216` `RecoverStaleOnce`.
- Heartbeat while running: `:543` `heartbeatDriverRun`.

### Task runs (finite agent work)

```text
ready task -> TaskRun.Create -> ClaimQueued(lease+fencing) -> executor
           -> heartbeat -> artifacts -> Complete/Finish/Requeue
```

- `TaskWorker.RunOnce`, `internal/driver/task_worker.go:48`.
- Claim: `internal/driver/task_request.go:308` and `:561`
  (`TaskRuns().ClaimQueued`).
- Create: `task_request.go:538`.
- Heartbeat: `task_request.go:860`, plus the background loop
  `heartbeatTaskRun` at `task_worker.go:256` (started at
  `task_request.go:734`, inside `startClaimedTaskRunHeartbeat` at `:731`).
- Complete / Finish: `task_request.go:913`, `:948`.
- Retry: `internal/driver/task_retry.go:54` (`TaskRuns().Requeue`).
- Placement descriptor: `TaskWorker.runnerPlacement`, `task_worker.go:171`.

### Scheduling and placement

`internal/driver/task_scheduling.go` is the whole scheduler. It is
predicate-based, not a queueing system:

- `verifyTaskRunRequestSchedulable` (`:24`) — refuses to enqueue work no
  node can run.
- `taskRunRequestSchedulingProfile` (`:54`) — resolves the
  `domain.WorkerProfile` that supplies requirements.
- `taskRunNodeSatisfiesScheduling` (`:95`) — the node predicate:
  `DrainState == NodeDrainActive` (`:99`), provider advertised (`:113`
  `nodeAdvertisesProvider`), capabilities superset (`:137`
  `stringListContainsAll`), heartbeat not expired.
- `resolveTaskProviderProfile` (`:197`) — picks the provider profile,
  taking host-bridge availability into account.

Drain state gates *admission* only. There is no priority queue, fairness,
drain-aware rebalancing of already-placed work, or quota enforcement in this
file.

### Sandboxing

The runtime seam is launch-only.

- `sandbox.SandboxLauncher` — `internal/driver/sandbox/launcher.go:83`,
  one method: `Launch(ctx, LaunchSpec) (SandboxProcess, error)`.
- `sandbox.SandboxProcess` — `launcher.go:106`, `Wait` / `Kill`.
- `LaunchSpec` — `launcher.go:91-102`. Env is passed verbatim; launchers
  must not add or drop entries.
- `sandbox.IsolatingLauncher` — `internal/driver/sandbox/policy.go:43`,
  marks launchers whose runtimes actually isolate.
- Selection: `ResolveSandboxLauncher()`, `internal/driver/executor.go:69`,
  called from `buildDriverExecutor` (`internal/cli/serve/serve.go:358`).
  `LOOM_DRIVER_SANDBOX=container` selects rootless containers
  (`internal/driver/sandbox/container.go`); the default is the local
  node-process launcher. An invalid sandbox configuration **disables the
  executor** rather than silently degrading isolation
  (`serve.go:359-362`).
- Egress policy: `internal/driver/sandbox/egress.go`; default resolves from
  the run's trust level — trusted → all, anything else → serve-only, fail
  closed (`launcher.go:98-101`, `serve.go:367-370`).

### Remote sandboxes: Daytona

There is no E2B implementation. `RuntimeProviderE2B`
(`internal/domain/control_plane.go:25`) is an unused enum value.

The ephemeral remote provider that shipped is Daytona:

- `internal/driver/bundled_runner.go:16-20` —
  `DaytonaTaskRunnerEntrypoint = "daytona-task-runner"`, which "provisions a
  Daytona sandbox, clones the repo, and runs the agent inside it (host-side
  harness driving the sandbox)". Selected via
  `LOOM_DAEMON_LEAF_RUNNER=daytona-task-runner`.
- `internal/workflows/builtin/daytona-task-runner.ts` — the runner itself.
- `internal/driver/task_bridge.go` — the host bridge the runner talks back
  through.
- `internal/runtimepreflight/preflight.go` — preflight gating.

Local, OpenShell, and GitHub-review runners live beside it:
`internal/workflows/builtin/{local,openshell,github-review}-task-runner.ts`.

## Serve-side background loops

All in `internal/cli/serve/serve_loops.go`, all `RunOnce`-per-tick,
context-cancel exit. None of them is gated behind `LOOM_DRIVER_EXECUTOR` —
they are server policy and run whenever serve has a store.

| Loop | Start fn | Implementation |
|---|---|---|
| Stale TaskRun sweeper | `serve_loops.go:30` | `driver.StaleTaskSweeper`, `internal/driver/stale_task_sweeper.go`. Default max heartbeat age 20 min (`:18`), sized for Daytona provision + clone + agent run; override with `LOOM_DRIVER_STALE_TASK_MAX_AGE`. |
| Outbox dispatcher | `serve_loops.go:63` | `driver.OutboxDispatcher`, `internal/driver/outbox_dispatcher.go`. Lead-notification delivery with retry/backoff. |
| Await-timeout sweeper | `serve_loops.go:177` | `driver.AwaitTimeoutSweeper`, `internal/driver/await_timeout_sweeper.go`. |
| Cron / delivery-retry / issue-journal bridge | `serve_loops.go` | See the file header comment at `:1-8`. |

The driver executor and task workers themselves ARE gated:
`startDriverExecutorIfEnabled` (`internal/cli/serve/serve.go:304`) checks
`LOOM_DRIVER_EXECUTOR` (default on, `serve.go:47,126`).
`startDriverTaskWorkers` (`serve.go:390`) launches one goroutine per
concurrency slot, each with a distinct `RunnerID`.

## HTTP surface

### Task-run API — the runner-facing contract

Op-dispatch, not one route per verb:

```text
POST /api/workspaces/{ws}/task-run/{op}
PUT  /api/workspaces/{ws}/task-run/artifacts/{artifactId}/content
```

`internal/webui/handlers/taskrunapi/module.go:145,148`. Ops registered at
`module.go:106-117`: `get`, `task-get`, `heartbeat`, `log-append`,
`complete`, `runtime-credential`, `artifact-declare`, `artifact-get`,
`artifact-list`, `artifact-finalize`.

Auth is a per-task-run **lease-token bearer**. The request identity is the
`{taskRunID, nodeID, leaseID, leaseToken, fencingToken}` tuple
(`module.go:151-159`), matching the fenced checks fleet-db performs.

Client contract: `@loom/sdk/runner` — see `sdk/README.md` and
`sdk/api-surface.v1.json`.

### Driver API — the workflow-facing contract

```text
POST /api/workspaces/{ws}/driver/{op}
GET  /api/workspaces/{ws}/driver/watch/epic
POST /api/workspaces/{ws}/driver/events/await
GET  /api/workspaces/{ws}/driver/events/awaits
POST /api/workspaces/{ws}/driver/workflows/start
POST /api/workspaces/{ws}/driver/workflows/await
```

`internal/webui/handlers/driverapi/module.go:191-198`. Client contract:
`@loom/sdk/driver`.

### Workflow run reads

```text
POST /api/workspaces/{ws}/workflows/{name}/versions
POST /api/workspaces/{ws}/workflows/{name}
GET  /api/workspaces/{ws}/runs/{runId}
GET  /api/workspaces/{ws}/runs/{runId}/events
GET  /api/workspaces/{ws}/runs/{runId}/stream
```

`internal/webui/handlers/workflows/module.go:39-43`. Note that
`/runs/{runId}` here is a **driver run**, not a task run — a common
confusion, because the older design docs used `/runs/{run}` for the
task-run concept.

### Workspace resolution

`{ws}` is canonicalized before it reaches a handler.
`internal/webui/server/middleware/workspace.go:18-21` defines
`WorkspaceRef{RequestedID, CanonicalID}`; `WorkspaceResolved` (`:77`)
resolves the raw route value and `WithWorkspaceRef` (`:54-62`) injects the
canonical key as the context workspace.

## Local state

Local, machine-specific state is deliberately *not* in fleet-db and is
deliberately *not* load-bearing:

- `bootstrap.StateCache`, `internal/bootstrap/statecache.go:33-52` —
  `{Version, LastWorkspace, Workspaces[key]{Path, Repos, Agents}}`. Its own
  docstring (`:30-32`): "All of these are regenerable... The cache is a
  convenience, never load-bearing for correctness."
- Resolution helpers: `internal/localworkspace/localworkspace.go:28-30`
  (`RepoPath`).
- Fleet-db-backed workspace view for older command code:
  `internal/cli/config/config.go:22-23`, `LoadConfig` at `:120-134`,
  overlaying local paths from `bootstrap.LoadStateCache` at `:180`.

`~/.loom/config.yaml` and `loom.yaml` are no longer runtime sources. The
only remaining `config.yaml` mentions in Go are a comment
(`internal/types/enums.go:50`) and a lock-file name
(`internal/configlock/configlock.go:16-17`). `gopkg.in/yaml.v3` is still a
direct dependency (`go.mod:40`) and the config structs still carry `yaml:`
tags.

## Known gaps

Verified absent, so you do not go looking:

- No `RuntimeProvider` Go interface. The seam is launch-only
  (`SandboxLauncher`).
- No `loom checkout` command; no checkout HTTP routes. Local path binding
  is `StateCache` only.
- No `Campaign` / `CampaignRun` / `CampaignStep` types or routes.
- No `CheckoutBinding` / `WorktreeBinding` / `LocalNodeConfig` /
  `SecretBinding` types.
- No HTTP surface for agent services — CLI only
  (`internal/cli/serve/worker/service_cmd.go`).
- `loom agentdef` and `loom worker profile` are still two separate public
  surfaces over overlapping concepts.
- The fleet worker claim path (`internal/webui/fleet`) still runs on the
  local daemon RPC pool (`handlers_claim.go:13-15`,
  `module.go:20`) plus Redis (`result.go:9`), separate from the
  lease/fencing path described above.

## Related

- `docs/design/distributed-control-plane.md` — the conceptual model
  (global/local/observed, lease semantics, push-vs-pull, security
  identities). Still current as *concept*; its command names and phase plan
  are annotated as stale.
- `docs/design/distributed-control-plane-data-model.md` — the superseded
  record-shape proposal, with a "Shipped As" table pointing back here.
- `docs/design/fleetdb-agent-platform-v2-proposal.md`,
  `docs/design/fleetdb-agent-platform-v2-phased-delivery.md`,
  `docs/design/fleetdb-agent-platform-v2-execution-topology-addendum.md` —
  the 2026-06-03 platform direction.
- `docs/design/driver-op-http-api.md` — the driver-op HTTP module in
  detail.
- `docs/design/taskrun-queue-and-worker-pool.md` — the task-run queue and
  worker-pool topology in detail.
- `docs/design/fleet-http-connection-reuse.md` — the tuned HTTP client
  fronting fleet-db.
- `sdk/README.md`, `docs/product/loom-typescript-sdk-spec.md` — the client
  contracts for the driver and task-run APIs.
- `docs/loom-glossary.md` — disambiguates "worker", "control plane",
  "fleet", "lead".
