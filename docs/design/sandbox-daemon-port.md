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

## D. Task-state transport: the git-native premise no longer holds ⚠️

The original PR #20 design said *"Beads state travels through git — no separate sync needed"*:
task state lived in `.beads/issues.jsonl` **in the repo**, so cloning the branch carried the
tasks into the container and the agent's `bd sync` + push carried updates back.

v5 moved task state into **FleetDB** (`issue_backend: fleetdb`); there is **no task state in
git** anymore. So git only carries *code* changes back — the `bd sync` step is gone (removed
from the one-shot bootstrap). The in-container `loom task` must instead reach the **FleetDB /
`loom serve` API over the network** (setting `LOOM_SERVER_URL` auto-selects the `api` issue
backend — see `internal/cli/issue_backend_resolve.go` + `deps.go`).

**Wired (one-shot):** the bootstrap now `export`s `LOOM_SERVER_URL` + `LOOM_WORKSPACE` into the
container, resolved on the host from `LOOM_SANDBOX_SERVER_URL` (explicit, container-reachable) or
a localhost→`host.docker.internal` rewrite of `LOOM_SERVER_URL`, with the active workspace ID.
`runSandboxOneshot` **fails fast** if neither a server nor a workspace can be resolved, instead
of booting a sandbox whose agent could never claim work.

**Still operator/environment-dependent (not verified against a live gateway):**
- **Reachability:** the host-gateway address (`host.docker.internal`) is a Docker default; the
  real OpenShell host address may differ — hence the `LOOM_SANDBOX_SERVER_URL` override.
- **OPA policy port:** the default "open" policy opens only 443/80/22, so a serve on `:8080`
  needs the port added to the sandbox network policy.
- **Auth:** the http client does OIDC device flow when serve requires auth — that can't run
  headless in a non-TTY container, so an auth'd serve needs a pre-minted/scoped token (TODO).
- **Daemon-mode** must do the same injection at the supervisor seam (§A).

## E. Live validation against OpenShell v0.0.53 (empirical) — Podman on macOS

Installed OpenShell 0.0.53 (Homebrew) + Podman driver + gateway on macOS/arm64 and drove
real sandboxes. Confirmed and fixed (one-shot, PR #118):

- **Arch vs OS.** A sandbox is **Linux**; the M-series host is **darwin/arm64**. Proven in a
  live sandbox: a `GOOS=linux` ELF prints `hello from linux/arm64` (rc 0); the darwin Mach-O
  gets `Exec format error` (rc 126). Same arch — only the OS/format differs.
- **F1 — upload a linux loom build.** `os.Executable()` on a Mac host is a darwin binary that
  can't run in the sandbox. Fixed: `resolveSandboxLoomBinary()` uses `LOOM_SANDBOX_LOOM_BIN`
  (a `GOOS=linux` build) or the running binary only on a Linux host. Better long-term: **bake
  loom into the `--from` image** (also dodges the upload issue below).
- **F2 — `create` must carry a command.** `openshell sandbox create` with **no** trailing
  command attaches an interactive SSH shell (`ssh -tt`) and blocks forever in a non-TTY
  context. Fixed: create ends with `-- true` (sandbox persists without `--no-keep`).
- **F3 — `exec` rejects newline args.** `openshell sandbox exec -- sh -c '<multi-line>'` →
  `InvalidArgument: command argument contains newline`. Fixed: upload the bootstrap as a file
  and run `exec -- sh /sandbox/bootstrap.sh` (validated live: a multi-line bootstrap runs by
  path where inline was rejected).
- **Large-file upload is flaky.** A 50 MB loom upload hit `ssh … broken pipe`; small files
  (≤2.4 MB) upload fine. Reinforces **baking loom into `--from`** over uploading a big binary.
- **Host address (driver-aware).** Podman exposes the host to sandboxes as
  **`host.containers.internal`** (gateway log: `grpc_endpoint=https://host.containers.internal:17670`)
  — not `host.docker.internal`/`192.168.127.254`. §D's `LOOM_SANDBOX_SERVER_URL` should be set per driver.

### Full task E2E — PROVEN (Podman/macOS)

A real loom binary inside an OpenShell sandbox claimed + closed a live task in the host's
FleetDB. The working recipe (what loom must generate for daemon-mode):

1. **Host serve** exposed to sandboxes: `loom serve --bind 0.0.0.0 -p 18099` (isolated
   `LOOM_CONFIG_DIR`); seed a workspace + tasks (`test/playground/setup.sh`).
2. **Loom-baked sandbox image** (F1): `FROM <openshell base>` + `COPY --chmod=0755 loom …`
   (a `GOOS=linux GOARCH=arm64` build; `--chmod` because the base runs as non-root `sandbox`).
3. **Policy opening the serve port** — concrete hosts only (a wildcard `host: "**"` crashes
   provisioning); bare `{host, port}` = L4 allow (all methods):
   ```yaml
   network_policies:
     loom_serve:
       name: loom-serve
       endpoints: [ { host: host.containers.internal, port: 18099 }, { host: "192.168.127.254", port: 18099 } ]
       binaries:  [ { path: /usr/local/bin/loom }, { path: /usr/bin/curl } ]
   ```
4. `openshell sandbox create --from <image> --policy <policy> -- true`, then
   `exec -n <s> -- env LOOM_SERVER_URL=http://host.containers.internal:18099 LOOM_WORKSPACE=<ws> /usr/local/bin/loom data claim|close …`.

Verified: `data list` returned the seeded tasks; `data claim PLAYGROUND-1` → host `in_progress`;
`data close PLAYGROUND-1` → host `closed`.

Not yet assembled into the single `loom task <wt> --sandbox` command — see §F.

## F. Literal `loom task --sandbox` end-to-end — remaining gaps (found by driving it)

Running the verbatim command (`loom task playground-coder --sandbox` with `LOOM_SANDBOX_LOOM_BIN`,
`LOOM_SANDBOX_SERVER_URL`, `LOOM_SANDBOX_POLICY`, `LOOM_SANDBOX_PROVIDERS=`, `LOOM_WORKSPACE`)
resolved the agent worktree and entered `runSandboxOneshot`, then stopped at:

    Error: could not determine git remote URL for …/worktrees/repo/playground-coder

The §D/§E core (in-sandbox agent ⇄ host FleetDB) is proven; the *full* one-command run
additionally needs the following — **two are one-shot code/design changes**, the rest infra:

1. **[code] repoURL must be sandbox-reachable.** The one-shot pushes to `origin` and hands that
   same URL to the sandbox to clone — but host and sandbox reach the host at different addresses
   (`127.0.0.1` vs `host.containers.internal`). Needs the same host-gateway rewrite as
   `LOOM_SANDBOX_SERVER_URL` (or a `LOOM_SANDBOX_REPO_URL` override) — the §D fix, applied to the clone URL.
2. **[code] bootstrap worktree layout.** The bootstrap runs `loom task worktrees/<name>` inside the
   fresh clone, but a clone has no `worktrees/<name>` (worktrees are local, not committed). A v2-era
   assumption that doesn't fit clone-fresh on v5 — rework to run in the repo root / `worktree add`.
3. **[infra] writable git endpoint** reachable from both host and sandbox (`git daemon
   --enable=receive-pack` on a both-reachable address, e.g. the host LAN IP).
4. **[infra] agent backend in the `--from` image** (e.g. the playground backend) + a selector
   (`LOOM_SANDBOX_BACKEND` → `cfg.Backend`).
5. **[infra] OPA policy** must also open the git port (alongside the serve port).

So the literal command is a feature-completion effort (esp. #1–#2 in code), not just assembly;
the novel risk (in-sandbox agent ⇄ host FleetDB over the OPA boundary) is already retired (§E).

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
4. FleetDB connectivity for the in-container agent (§D): host-gateway URL + scoped token + OPA
   port allowance — **required before either mode can actually fetch work.**
