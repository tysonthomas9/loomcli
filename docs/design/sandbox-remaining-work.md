# OpenShell sandbox — least-privilege implementation record (RW1–RW6 + 2C)

Status record for the OpenShell sandbox least-privilege feature. The original
forward-looking plan lived here; this now records **what actually shipped** (the
architecture diverged from the first RW1/RW2 sketch — see "Architecture" below).
Mechanics detail + the v0.0.53 field notes remain in `sandbox-daemon-port.md`
(§A–§F); the one-shot code is `internal/cli/agent/sandbox_oneshot.go`.

**Shipped across two PRs (both gated, tested, CI-green):**
- **fleet-db #76** (`feat/sandbox-rbac-enablement`, base `origin/main`) — activates
  fleet-db's RBAC: admin-seed bootstrap (`auth.SeedAdmin` + `Auth.BootstrapAdminActor/Key`),
  the workspace-scoped meta route `GET /api/v1/{workspace}/workspace`, and the atomic
  `ProvisionScopedKey`/`RevokeKey` admin apikey endpoint (key + ACL role in one MULTI, co-TTL).
- **loom #126** (`feat/sandbox-loom-rbac-config`, stacked on **#118** the one-shot, base v5) —
  the loom side (RW1–RW6 + 2C) below.

Original v2 code preserved at tag `rescue-sandbox-openshell-pr20`. fleet-db #75 added
`execution`/`execution_config` to the Agent model (consumed by RW6).

## Architecture (as shipped — one endpoint, one scoped credential)

**`loom lead` + the host daemon stay admin on the trusted host; a sandboxed
planner/worker gets a workspace-scoped `developer` API key, enforced by fleet-db
RBAC.** The container opens exactly one socket and carries one credential whose
scope fleet-db enforces uniformly.

- **Transport = Opt 1 (direct to fleet-db via `ModeCloud`).** The bootstrap exports
  `LOOM_FLEET_DB_URL/_API_KEY/_ACTOR` + `LOOM_WORKSPACE` and **drops `LOOM_SERVER_URL`**,
  so both config resolution (`OpenStore` → `ModeCloud`) and the `fleetdb` issue backend
  hit the same fleet-db with the same scoped key. (This supersedes the original RW1/RW2
  sketch, which routed through `LOOM_SERVER_URL`/the API backend and self-resolved
  worktrees — neither shipped.)
- **2C (network isolation layer, shipped):** point `LOOM_SANDBOX_FLEETDB_URL` at a
  `loom serve` running the config proxy and the sandbox reaches **only serve**; serve
  forwards the caller's scoped key to fleet-db (RBAC still enforces). See RW-2C.

## What shipped (per workstream)

### RW1 + RW-SEC — least-privilege transport & credential  `[loom #126]`
- `fleethttp.Auth.Apply` dual-sends `X-API-Key` (+ legacy `X-Fleet-API-Key`); fleet-db
  reads `X-API-Key`.
- **Scoped config resolution:** `store.ScopedWorkspaceGetter` (`GetWorkspaceScoped` →
  `GET /api/v1/{ws}/workspace`) + a `ModeCloud` branch in `config.ResolveActiveWorkspace`
  (`internal/cli/config/config.go`) that resolves *only* the active workspace via the
  scoped route and returns early — never the global `Workspaces().List()`. The `fleetdb`
  issue backend skips its global existence-check on the cloud path (`deps.go`). Net: a
  `developer` key with **no global role** resolves workspace + daemon config + claims/closes.
- **Credential provisioning** (`sandbox.ProvisionCredential`, `internal/sandbox/credential.go`):
  when the host holds an admin key, mints a short-TTL (`CredentialTTLSeconds` = 6 h),
  workspace-scoped `developer` key for a unique actor (`sandbox:<ws>:<agent>:<ts>`) and
  returns a revoke func; no-op ambient passthrough in dev/auth-off. Shared by the one-shot
  and the daemon strategy.

### RW2 — repo materialization  `[loom #126]`
`RepoConfig.RemoteURL` is projected from the fleet-db Repo row (`repoConfigFromStore`), so
a sandbox clones from a container-reachable remote (host-gateway rewrite) and pushes work
back. (No worktree self-resolution — the bootstrap clones explicitly.)

### RW3 — driver-aware reachability  `[loom #126]`
`sandbox.HostGateway()` resolves Podman → `host.containers.internal`/`192.168.127.254`,
Docker → `host.docker.internal` (via `OPENSHELL_DRIVERS`; `LOOM_SANDBOX_HOST_GATEWAY`
override). Applied to both the fleet-db URL and the repo clone URL. fleet-db must bind
`0.0.0.0` (the gvproxy gateway can't reach a `127.0.0.1`-bound server).

### RW4 — auto-generated OPA policy  `[loom #126]`
`sandbox.PolicyEndpoints` + `sandbox.WritePolicy` generate a policy opening the fleet-db
(+ git) endpoints and pass `--policy` from both spawn paths when no explicit
`LOOM_SANDBOX_POLICY` is set. **Format (v0.0.53, validated live):** top-level
`version: 1`; a `filesystem_policy` granting the default read/write surface; concrete
hosts only (a wildcard `host: "**"` crashes provisioning); each Podman host also gets its
`192.168.127.254` alias; `binaries` lists the loom + git + curl paths.

### RW5 — deliver loom via a `--from` image  `[loom #126]`
`Config.LoomBinPath` (from `LOOM_SANDBOX_LOOM_PATH`) → "loom baked in image, skip upload".
Large-file `sandbox upload` is flaky, so baking is preferred; `LOOM_SANDBOX_BACKEND`
selects the agent backend.

### RW6 — daemon-mode (supervised `execution: sandbox` agents)  `[loom #126]`
- **§B plumbing:** `execution`/`execution_config` threaded through `domain.Agent`, the
  fleetdb agent wire, store `Agent{Create,Update}`, `config.AgentEntry` (+ `Equal`/
  `agentEntryFromDomain`), and `loom agentdef add --execution`.
- **§A strategy:** the supervisor's `buildCommand` dispatches `IsSandbox()` agents to
  `buildSandboxCommand`, which marshals the `AgentProcess` into an
  `agent.SandboxExecSpec` and calls `agent.BuildSandboxExecCommand` (push branch →
  `create -- true` → upload loom + bootstrap → return the `exec` cmd; clone-from-`RemoteURL`
  bootstrap runs loom **without** `--daemon-mode`). `postExitCleanup` → `cleanupSandbox`
  does **non-destructive** recovery (fetch + ff-merge + revoke + delete; a non-ff is logged,
  not deleted). The heavy sandbox/fleet-db logic lives in `internal/cli/agent` (reached via
  the existing supervisor→agent import) to keep package import fan-out under the gate.
- **§C liveness:** sandbox agents skip the host transcript-mtime watchdog (the transcript
  is in the container) and fall back to log-mtime — the fix for the watchdog-FATAL class.
- **All daemon-sandbox behavior is gated on `Entry.Execution == "sandbox"` (default off).**

### RW-2C — loom-serve config proxy (network isolation)  `[loom #126]`
`loom serve --fleet-db-proxy-url <fleet-db>` (env `LOOM_FLEET_DB_PROXY_URL`) exposes a
reverse proxy at `/api/v1/` (`internal/webui/fleet_proxy.go`) that forwards to fleet-db,
passing the caller's `X-API-Key`/`X-Actor` through unchanged — serve injects **no** identity,
so fleet-db's RBAC authorizes the caller's scoped key. The route is exempt from serve's JWT
auth (`isPublicRoute`, same pattern as `/api/fleet/`). Point `LOOM_SANDBOX_FLEETDB_URL` at
serve and the sandbox reaches only serve; the auto-generated OPA policy then opens serve's
port instead of fleet-db's. No agent-side code change — the `ModeCloud` client works
unchanged against serve.

## Bugs found by the live E2E (fixed in #126, would have shipped broken)

Unit tests were green; only driving the full path in a real sandbox surfaced these:
1. **`sharedTransport` ignored the egress proxy (showstopper).** `internal/backend/fleet/transport.go`
   built a bare `&http.Transport{}`, defaulting `Proxy` to nil — so a sandboxed loom bypassed
   OpenShell's mandatory egress proxy (`http_proxy=http://10.200.0.1:3128`) and every fleet-db
   dial was refused. Fix: `Proxy: http.ProxyFromEnvironment` (no-op outside a sandbox). Without
   this, **no** sandboxed loom could reach fleet-db.
2. **RW4 policy missing `version: 1`** → v0.0.53 rejects the policy ("missing field `version`")
   → sandbox create fails.
3. **RW4 policy dropped `filesystem_policy`** → nothing writable in-container (`/sandbox`, `/tmp`,
   `/home` all read-only) → clone/upload fail.

## Verification (the proven recipe — reuse as the integration test)

Run against fleet-db with **authz ON** (production `X-API-Key` path, not `--auth-dev-mode`):
1. fleet-db bound `0.0.0.0:<port>` with `-redis-durability-profile managed` (skips `CONFIG SET`,
   so miniredis works) and `FLEET_AUTH_BOOTSTRAP_ADMIN_ACTOR/_KEY` set (admin-seed).
2. Create a workspace; mint a scoped `developer` key via `POST /api/v1/admin/apikeys`
   (`{workspace, role: developer}`).
3. Build a `GOOS=linux` loom (or bake it via `--from`); generate the OPA policy (RW4).
4. **One-shot / data-path E2E:** in an OpenShell sandbox, `loom data create|claim|close` over
   `ModeCloud` with the developer key → host-side confirms the transition. **Least-privilege
   battery:** scoped read 200, `/api/v1/admin/*` 403, no-key 401, external host blocked (the
   egress proxy returns 403 for a disallowed endpoint).
5. **2C E2E:** repeat with `LOOM_SANDBOX_FLEETDB_URL` pointed at `loom serve --fleet-db-proxy-url`,
   OPA policy opening only serve → same lifecycle, fleet-db egress blocked.

(Proven 2026-06 on Podman/macOS, both the direct-fleet-db and 2C-via-serve paths.)

## Remaining (env-gated, not code-blocked)

**Daemon-mode live drain with a real AI agent.** The RW6 §A/§C supervisor strategy is
unit-tested and the data path is live-proven, but a fully end-to-end daemon-supervised
sandbox agent additionally needs an AI backend *inside* the container — which requires the
generated OPA policy to also open the AI-provider endpoints (today it opens only fleet-db +
git), or the token-free `playground` backend (which has its own drain blockers). Left as a
follow-up.

## Bring up the OpenShell test stack (macOS/arm64, no Docker)
- `brew install podman && podman machine init && podman machine start`
- Install the OpenShell CLI + gateway (brew services).
- `~/.config/openshell/gateway.env`:
  `OPENSHELL_DRIVERS=podman` · `PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin` ·
  `OPENSHELL_PODMAN_SOCKET=<podman machine inspect → ConnectionInfo.PodmanSocket.Path>`
- `brew services restart openshell` → `openshell status` should show **Connected** (v0.0.53).
