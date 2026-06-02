# OpenShell sandbox — remaining work (runnable plan)

Actionable, dependency-ordered plan to finish the OpenShell sandbox feature on v5.
Evidence + detail for every claim here is in `sandbox-daemon-port.md` (§A–§F);
the one-shot code is `internal/cli/agent/sandbox_oneshot.go`.

Branch: `feat/sandbox-openshell-v5` (loomcli PR #118). Server field: fleet-db PR #75.
Original v2 code preserved at tag `rescue-sandbox-openshell-pr20`.

## Already done (don't redo)
- One-shot `loom task|plan <wt> --sandbox` ported to v5 + reconciled with the live
  OpenShell **v0.0.53** CLI: `create -- true` → `sandbox upload` loom → `sandbox upload`
  bootstrap → `exec -- sh /sandbox/bootstrap.sh` (F1/F2/F3, §E). Build/vet/golangci/guards green.
- Env knobs: `LOOM_SANDBOX_LOOM_BIN`, `LOOM_SANDBOX_SERVER_URL`, `LOOM_SANDBOX_REPO_URL`,
  `LOOM_SANDBOX_POLICY`, `LOOM_SANDBOX_PROVIDERS`, `LOOM_SANDBOX_BACKEND`.
- fleet-db #75: `execution` + opaque `execution_config` fields on the Agent model.
- **Proven E2E**: a real loom binary in an OpenShell/Podman sandbox claimed → closed a task
  in the host FleetDB (the §E recipe). The novel risk (in-sandbox agent ⇄ host FleetDB over the
  OPA boundary) is retired.

## Remaining work (in dependency order)

### RW1 — ROOT BLOCKER: API-backed workspace/daemon-config resolution  [v5 config-layer]
Today `config.ResolveActiveWorkspace` / `config.LoadDaemonConfig`
(`internal/cli/config/project.go`) load workspace + daemon config from a **local fleet-db
store** (`bootstrap.OpenStore`). A sandbox (or any remote-only host) has no local store, so the
agent dies at `load workspace config: open fleet-db store: fleet-db binary not found`. The API
backend serves *tasks*, not *workspace config*. (Root cause: v5 moved config out of the repo,
`loom.yaml` → FleetDB, so there is no longer any local-or-cloned source of workspace config.)
- **Do:** when `LOOM_SERVER_URL` is set, resolve workspace + roles + agents + repos + daemon
  settings via the **API backend** (HTTP to serve) instead of the local fleet-db store.
- **Done when:** on a host with NO local fleet-db data, `LOOM_SERVER_URL=… LOOM_WORKSPACE=… loom task <agent>` resolves the workspace and reaches task selection.
- **Note:** this is the true prerequisite for *any* remote/sandboxed agent — bigger than sandbox.

### RW2 — Rework the one-shot bootstrap to v5's worktree/repo model  [depends on RW1]
The bootstrap (`buildOneshotCommand`) does `git clone <repoURL> → loom task worktrees/<name>`.
v5 keeps worktrees in the loom config dir (`<cfg>/workspaces/<ws>/worktrees/<repo>/<agent>`),
not inside the repo, and config isn't in the repo — so the manual clone + `worktrees/<name>`
path is v2-era and wrong.
- **Do:** let loom set up the worktree/repo from the workspace's repo config (after RW1).
  Bootstrap becomes ~`/sandbox/loom <role> <agent> --daemon-mode --backend <b>` with
  `LOOM_SERVER_URL`/`LOOM_WORKSPACE` exported; drop the manual `git clone`/`worktrees/<name>`/push.
- **Done when:** the in-sandbox agent self-resolves its worktree and works a task with no manual clone.

### RW3 — Repo + serve reachable from the sandbox (driver-aware)
- Serve: `loom serve --bind 0.0.0.0`. Sandbox reaches the host at the **driver's** address:
  Podman → `host.containers.internal` (`192.168.127.254`); Docker → `host.docker.internal`.
  `LOOM_SANDBOX_SERVER_URL` overrides; make `sandboxHostGateway` driver-aware (it's hardcoded
  `host.docker.internal`).
- Repo: the workspace repo's `Remote` must be reachable from the sandbox (a git endpoint on the
  host gateway, or a real remote), and **writable** for push-back. `LOOM_SANDBOX_REPO_URL` overrides.

### RW4 — Auto-generate the OPA policy
The default "open" policy opens only 443/80/22; the sandbox needs the **serve port** (+ git port).
- **Do:** loom generates a policy opening the serve + repo endpoints and passes `--policy`.
  Format rules (proven, §E): **concrete hosts only** (a wildcard `host: "**"` crashes
  provisioning); a bare `{host, port}` endpoint = L4 (all methods); `binaries` lists the loom +
  git binary paths. `LOOM_SANDBOX_POLICY` overrides today; auto-gen is the goal.

### RW5 — Deliver loom + backend via a `--from` image (not upload)
Large-file `sandbox upload` is flaky (50 MB broke); bake instead.
- **Do:** build/select a `--from` image with a `GOOS=linux` loom + the agent backend baked
  (`COPY --chmod=0755 …`; the base runs as non-root `sandbox`). Add "loom already in image →
  skip the upload step". `LOOM_SANDBOX_BACKEND` selects the backend.

### RW6 — Daemon-mode (supervised sandbox agents)  [depends on RW1–RW5 + fleet-db #75]
- Consume `execution`/`execution_config` (fleet-db #75) → plumb through `domain.Agent` →
  `agentWire` → `config.AgentEntry` (see `sandbox-daemon-port.md §B`).
- Supervisor `ExecutionStrategy` seam (§A): `BuildSpawnCommand`/`Cleanup`/`OnStop` hooked into
  `supervisor/spawn.go`, `health.go`, agent construction in `supervisor.go`.
- **§C liveness adaptation (critical):** IPC heartbeats, ownership leases, and host-side
  transcript liveness can't reach into a container — for `execution: sandbox` agents, rely on
  log-mtime only and skip the IPC/lease/transcript watchdogs, or they get reaped.

## Verification harness (the proven recipe — reuse as the integration test)
1. `LOOM_CONFIG_DIR=<tmp> loom serve --bind 0.0.0.0 -p 18099` (isolated).
2. `LOOM_CONFIG_DIR=<same> bash test/playground/setup.sh` → seeds the PLAYGROUND workspace + tasks.
3. Build a loom-baked image: `FROM <openshell base>` + `COPY --chmod=0755 loom /usr/local/bin/loom`.
4. Policy opening `:18099` (concrete hosts) + binaries `/usr/local/bin/loom`,`/usr/bin/curl`.
5. `openshell sandbox create --from <img> --policy <p> -- true`; then
   `exec -n <s> -- env LOOM_SERVER_URL=http://host.containers.internal:18099 LOOM_WORKSPACE=PLAYGROUND /usr/local/bin/loom data claim|close <id>` → verify the transition host-side.

## Bring up the OpenShell test stack (macOS/arm64, no Docker)
- `brew install podman && podman machine init && podman machine start`
- `curl -LsSf https://raw.githubusercontent.com/NVIDIA/OpenShell/main/install.sh | sh` (CLI + gateway via brew services)
- `~/.config/openshell/gateway.env`:
  `OPENSHELL_DRIVERS=podman` · `PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin` ·
  `OPENSHELL_PODMAN_SOCKET=<podman machine inspect → PodmanSocket.Path>`
- `brew services restart openshell` → `openshell status` should show **Connected**.
