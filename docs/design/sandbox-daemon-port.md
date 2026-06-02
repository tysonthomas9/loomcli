# Daemon-mode OpenShell sandbox — port plan onto v5

Follow-up to the one-shot `--sandbox` work (PR #118). This documents what it takes
to let **daemon-supervised** agents run with `execution: sandbox` on v5, after the
v5 refactor moved daemon config into the FleetDB/domain store.

Companion docs: `openshell-sandbox-integration.md` (original design, full).
Original daemon `SandboxStrategy` code: tag `rescue-sandbox-openshell-pr20`.

## Headline

A **coordinated, typed, two-repo change**. There is **no freeform/metadata field**
on the Agent model in either loomcli or fleet-db, and daemon agent config
round-trips **loom ⇄ fleet-db over HTTP** (`internal/infra/fleetdb/agent.go`
`agentWire`). So a sandbox setting must be threaded through ~11 loomcli sites
**and** the fleet-db server. The work splits into three parts:

| Part | Size | Risk |
|---|---|---|
| A. Runtime seam (supervisor strategy) | small, mechanical | low |
| B. Config plumbing (loom + fleet-db) | larger, boilerplate | low per-site; cross-repo coordination |
| C. Control-plane liveness adaptation | medium | **HIGH — new in v5, see §C** |

## A. Runtime seam (loomcli supervisor)

v5 splits build from start: `buildCommand → spawnAgent(cmd.Start) → waitForAgent(cmd.Wait)`;
`StopAgent` does process-group SIGTERM→SIGKILL. Adapt the strategy to that split:

```go
type ExecutionStrategy interface {
    BuildSpawnCommand(ap *AgentProcess) (*exec.Cmd, error) // nil => use default loom cmd
    Cleanup(ap *AgentProcess, exitCode int) error          // sandbox: git fetch + ff-merge + delete
    OnStop(ap *AgentProcess)                                // sandbox: openshell sandbox delete
}
```

Hook points (all `internal/cli/daemon/supervisor/`):
- **resolveStrategy** at `AgentProcess` construction (`NewAgent` + the runtime task-add path).
  `ap.RepoConfig.ResolveRemote()`/`ResolveRemoteBranch()` + `Supervisor.ProjectDir` yield the clone URL.
- **BuildSpawnCommand** consulted in `spawnAgent` before `cmd.Start()`.
- **Cleanup** invoked in the `superviseAgent` loop right after `waitForAgent` returns
  (NOTE: there is **no** pre-existing empty hook — insert into the real exit path).
- **OnStop** appended at the tail of `StopAgent` after the kill.
- **Locking**: `ap.Mu` guards `Cmd/Pid/...`; strategy methods snapshot under the lock then
  do I/O unlocked (as the original `SandboxStrategy` did).

The `SandboxStrategy` body (openshell arg-building, loom-binary upload, fetch/merge-back) is in
the rescue tag and is mostly field renames (`ap.entry→ap.Entry`, `ap.cmd→ap.Cmd`). It can
**share the arg/command builders already written for the one-shot** (`internal/cli/agent/sandbox_oneshot.go`).

## C. The landmine: v5 control-plane assumes a host-local agent ⚠️

The main reason daemon-mode is non-trivial (this did not exist when PR #20 was written).
`spawnAgent` injects `LOOM_DAEMON_SOCKET` (IPC heartbeats), `LOOM_AGENT_LEASE_ID/TOKEN`,
`LOOM_AGENT_OWNERSHIP_*`, `LOOM_SESSION_ID`, and a host transcript path. The supervisor's
watchdog, ownership leases, and transcript-based liveness all depend on the agent process
talking back through those.

A **containerized** agent cannot: it can't reach the host IPC socket, can't refresh leases,
and writes its transcript *inside* the container. The host only sees the `openshell` process.
Consequence:
- log-mtime liveness probably survives (openshell forwards container stdout → daemon log mtime).
- IPC-heartbeat / lease-ownership / transcript liveness will NOT → the watchdog / claim-reaper
  would treat a healthy sandboxed agent as dead and kill it (the "watchdog FATAL" failure class).

**Mitigation:** for `execution: sandbox` agents, treat the `openshell` process as the liveness
unit — rely on log-mtime only and **skip IPC/lease/ownership watchdogs** for them. Design this
*before* enabling daemon-mode, or sandboxed agents get reaped.

## B. Config plumbing

**loomcli (~11 sites, mechanical):** `domain.Agent`; `store.AgentCreate`/`AgentUpdate`;
`fleetdb.agentWire` + `toDomain` + create/update bodies; `config.AgentEntry` + `Equal()` +
`agentEntryFromDomain`; `validateAgents`; `loom agentdef add` flags.
(`supervisor.AgentProcess` needs nothing — reads via `Entry`.)

**fleet-db (separate repo, `Daedaelius` auth):** `models.Agent`; `api/openapi.yaml` (Agent +
Create/Update schemas — **no new routes**, so openapi-coverage is satisfied by fields alone);
handler request structs; service `AgentUpdate`; **Redis marshal/unmarshal**. **Postgres needs
no migration** (whole agent stored as a JSON `data` blob).

**Scope-reducer (recommended):** carry it as **one opaque `execution_config json.RawMessage`**
blob + a typed `execution string`, rather than mirroring a fully-typed sandbox schema into
fleet-db. fleet-db just stores/echoes the blob; the sandbox schema lives only in loom and can
evolve without fleet-db PRs.

## Sequencing & effort

1. **fleet-db PR** (S–M): `execution` + `execution_config` → model, openapi, handlers, Redis
   marshal, validation. Merge first (loom's `agentWire` silently drops unknown fields).
2. **loom config PR** (M): thread the fields through the 11 sites + `agentdef add` flags.
3. **loom runtime PR** (M): strategy seam + port `SandboxStrategy` + `resolveStrategy`,
   **gated on `execution=="sandbox"` (default off)** + the §C liveness adaptation.

~3 PRs, multi-day, two CI suites. The seam is ~a day; the liveness design is the wildcard.

## Open decisions
1. Opaque `execution_config` blob vs. fully-typed sandbox schema in fleet-db? (recommend opaque)
2. Liveness for sandboxed agents — log-mtime-only + disable IPC/lease watchdogs (pragmatic), or
   forward a control channel into the container (robust, much larger)?
3. Is daemon-mode worth it now, or is the shipped one-shot enough near-term?
