# Remove Beads — Collapse to Fleet-Only Issue Backend

**Status:** Draft — awaiting review
**Date:** 2026-04-22
**Supersedes:** `docs/design/fleetdb-integration.md`
**Delivery:** New branch `remove-beads` off `v4`, five commits, one PR.

---

## 1. Goal

Fully remove the beads issue tracker from loomcli and make the remote
**fleet** HTTP backend the sole issue-data source. All `bd` CLI and
daemon dependencies are deleted; the vendored `third_party/beads/` tree
is removed; agents gain a new `loom issue …` command surface for
CRUD operations that routes through the parent daemon's IPC when
available.

## 2. Why now

Today the repo carries three overlapping "issue backend" names
(`beads`, `fleetdb`, `fleet`, plus `api`) with only one real shape
underneath: an embedded beads daemon. The `fleetdb-integration.md`
design doc described an in-process fleet-db storage layer that was
never built — the code shipped under the `fleetdb` name is still a
beads daemon with a miniredis alongside it for coordination locks.
That leaves:

- ~10k LoC of vendored beads source in `third_party/beads/`
- A `bd` binary built and shipped in every release tarball
- Multiple call sites shelling out to `bd` (init, doctor, workspace
  create, health doctor, monitor, migrate)
- Four backend constants with overlapping semantics
- Documentation (README, AGENTS.md, design docs) that instructs
  humans and agents to run `bd …` commands

Consolidating around fleet removes all of that, matches the direction
the team is already investing in (auth, device flow, HTTP API), and
gets the repo back to a single mental model.

## 3. Final state

### 3.1 Backend surface

- Single backend constant: `IssueBackendFleet = "fleet"` in
  `internal/cli/issue_backend_resolve.go`.
- `internal/backend/fleet/` stays — HTTP REST client against the fleet
  server's `/api/workspaces/{id}/…` endpoints.
- `internal/backend/api/` is folded into `internal/backend/fleet/`,
  keeping the `AuthTransport` + OIDC device-flow code path.
  `LOOM_SERVER_URL` becomes an alias for `LOOM_FLEET_URL`.
- Deleted: `internal/backend/beads/`, `internal/rpc/`,
  `internal/types/`, `internal/cli/cli_beads_adapter.go`,
  `internal/cli/migrate/`, `third_party/beads/`, repo-root `.beads/`.

### 3.2 Resolver & config

- `ResolveIssueBackendType()` returns `"fleet"` unconditionally.
  Retained as a thin constant getter so existing call sites do not
  need edits.
- `validIssueBackends` shrinks to `{"fleet": true}`. Unknown values
  produce a clear error listing only `fleet`.
- `DaemonSettings.IssueBackend` stays for config compatibility but
  accepts only `"fleet"` or empty.
- `daemon.fleetdb.*` block and the `FleetDBSettings` struct are
  deleted. Env vars `LOOM_FLEETDB_ENABLED`, `LOOM_FLEETDB_REDIS_URL`,
  `LOOM_FLEETDB_WORKSPACE` are removed (no deprecation period).
- `DefaultIssueBackend()` factory:
  - If `daemon.fleet.url` / `LOOM_FLEET_URL` is unset, the factory
    returns an error. CLI commands surface:
    ```
    Error: no fleet server configured.
      Set daemon.fleet.url in loom.yaml, or export LOOM_FLEET_URL.
      See docs/... for fleet server install instructions.
    ```
  - Otherwise constructs the merged fleet+api HTTP backend using
    `AuthTransport`.
- `DefaultDeps()` loses its `BD` / `BDRunner` field and the
  `cliBeadsAdapter` fallback.

### 3.3 Agent surface (new)

Net-new CLI commands under `loom issue <subcommand>`:

| Command | Description |
|---|---|
| `loom issue ready [--limit N] [--parent ID]` | List ready (unblocked) issues |
| `loom issue list [--status X] [--type X] [--assignee X] [--limit N]` | List issues by filter |
| `loom issue show <id> [--json]` | Show issue detail (dependencies, comments) |
| `loom issue claim <id>` | Atomically claim an issue for this agent |
| `loom issue create --title=... --type=X --priority=N [--parent ID] [--description=...]` | Create an issue |
| `loom issue update <id> [--status X] [--assignee X] [--external-ref URL]` | Partial update |
| `loom issue close <id> --reason="..."` | Close an issue |
| `loom issue reopen <id> [--reason="..."]` | Reopen a closed issue |
| `loom issue comment <id> "<text>"` | Add a comment |
| `loom issue stats [--json]` | Issue-count stats |
| `loom issue blocked [--json]` | Issues with open blockers |
| `loom issue dep add <id> --depends-on <id> [--type blocks\|parent-child]` | Add dependency |
| `loom issue dep remove <id> --depends-on <id>` | Remove dependency |
| `loom issue label add <id> <label>` | Add label |
| `loom issue label remove <id> <label>` | Remove label |

All commands delegate to `DefaultIssueBackend()`, which in turn
dispatches based on environment:

- If `LOOM_DAEMON_SOCKET` is set → use `ipcIssueBackend` (routes
  through the parent daemon's Unix socket). The daemon holds the
  fleet auth token; the agent's `LOOM_AGENT_NAME` is injected as
  actor metadata for audit.
- Otherwise → direct HTTP to fleet using the caller's own token.

Agents running inside `loom task` / `loom plan` / `loom lead` sessions
inherit `LOOM_DAEMON_SOCKET` and `LOOM_AGENT_NAME` automatically.

### 3.4 Fleet coordination carve-out

The existing `FleetDBServer` is a misnomer — it embeds a beads daemon
and runs miniredis alongside for coordination. We split the two:

- Rename `FleetDBServer` → `FleetCoordinator` (file renamed from
  `internal/cli/fleetdb_server.go` → `internal/cli/fleet_coordinator.go`).
- Strip the beads parts: drop `rpcServer`, `rpcClient`, `backend`
  fields, the `beads.NewServer` / `beads.NewSQLiteStorage` / imports.
- Keep `miniRedis`, `rdb`, `fleetStore`. This component exists only to
  back claim locks, JWT signing keys, terminal state persistence, and
  stale-server detection for `loom serve` in fleet mode.
- It no longer implements `backend.IssueBackend`. Only exposes
  `FleetStore() *fleet.Store`.
- `daemon_cmd.go` and `serve.go` stop obtaining the issue backend from
  `FleetCoordinator` — they construct it directly from config via the
  factory.

### 3.5 IPC layer (agent → daemon → fleet)

Preserved, repointed:

- `internal/backend/agentipc/` and `internal/cli/ipc_issue_backend.go`
  stay. Their wire protocol (JSON ops over Unix socket) is preserved.
- The daemon-side handler is rewritten to apply incoming ops via an
  in-process `fleet.FleetBackend` instead of a `beads.BeadsBackend`.
- The daemon holds the fleet auth token (obtained via OIDC device
  flow on first launch; cached per standard `httpclient` rules).
- Agent identity: `BD_ACTOR` → `LOOM_AGENT_NAME`. One-release alias:
  if `BD_ACTOR` is set and `LOOM_AGENT_NAME` is not, use `BD_ACTOR`
  with a deprecation warning. Removed in the release after.
- Audit: the daemon injects an `X-Loom-Agent-Name` header on every
  outbound fleet call originating from an agent socket.

## 4. Commit sequence

Each commit keeps `go build ./...` and `go test ./...` green.

### Commit 1 — `add loom issue subcommands`

**Net-new code, zero deletions.** The replacement surface ships before
anything is removed, so agents have a working path end-to-end.

- New package `internal/cli/issue/` (or `internal/cli/cmd/issue/`) with
  one file per subcommand group: `ready.go`, `list.go`, `show.go`,
  `claim.go`, `create.go`, `update.go`, `close.go`, `reopen.go`,
  `comment.go`, `stats.go`, `blocked.go`, `dep.go`, `label.go`.
- Root registration in `internal/cli/root.go`: `loom issue …`.
- Each command calls `cli.DefaultIssueBackend()` and emits output
  matching existing beads JSON contract (so downstream tools that
  parse `bd … --json` keep working when they switch to
  `loom issue … --json`).
- Unit tests per command using a mock `backend.IssueBackend`.
- A draft replacement for `AGENTS.md` is added as
  `docs/agents/AGENTS_FLEET.md` for review but not swapped in.

### Commit 2 — `remove bd subprocess callsites`

Delete every `exec.Command("bd", …)` / `execCommand(…, "bd", …)`. The
list (from exploration):

- `internal/cli/cli_beads_adapter.go` — entire file (~535 lines)
- `internal/cli/daemon_ensure.go` — entire file; `EnsureIssueBackendRunning`
  becomes a one-line no-op function living in `deps.go`
- `internal/cli/serve/serve.go:245` — `stopIssueBackend` deleted
- `internal/webui/health_doctor.go` — entire file deleted (stop/start/poll
  loop is beads-specific); SSE error banner replaced with a plain
  fleet-reachability check in the webui
- `internal/cli/monitor/monitor_collect.go:486` —
  `collectSyncBdStatusDeps` deleted; `MonitorData.SyncInfo.DBSynced`
  field dropped (no equivalent for fleet)
- `internal/cli/workspace/init_helpers.go` — `bd --version` check and
  `bd init` branch deleted
- `internal/cli/workspace/init.go` — `initBeadsInWorkspace` deleted
- `internal/cli/workspace/workspace_cmd.go` — `bd init` call deleted
- `internal/cli/serve/workspacemgr/workspace.go` — `initWorkspaceBeads`
  deleted (unconditional `bd init` + `bd repo add` gone)
- `internal/cli/doctor/doctor_checks.go` — `checkBdCLI`, `checkBdDaemon`,
  `checkBdSocket`, `checkBeadsInit` deleted; `internal/rpc` import removed
- `internal/cli/doctor/doctor.go` — the else-branch that appended bd
  checks is gone
- `internal/cli/migrate/` — entire package deleted (beads→fleet migration
  is not supported per project decision)
- `DefaultDeps()` in `internal/cli/deps.go` — remove `BD` field,
  `BDRunner` interface, `defaultBDRunnerImpl`, `newCliBeadsAdapter`
  fallback. Construct fleet backend via the factory; error out with
  the first-run message if fleet URL missing.
- Test deletions/edits: `cli_beads_adapter_test.go`,
  `daemon_ensure_test.go`, `worktree_beadsdir_test.go`,
  `config_validate_fleetdb_test.go`, `fleetdb_server_test.go` — start
  with delete; any pieces still applicable get rehomed.
- `integration_test.go` has 35 bd references — edit down to fleet-only
  integration; drop fixtures that seed beads data.

### Commit 3 — `repoint agent IPC from beads to fleet`

Surgical — does not delete beads yet, because the beads path is still
needed for the IPC op types we want to keep wire-compatible.

- `internal/backend/agentipc/backend.go` — the `Backend` struct still
  implements `backend.IssueBackend` by forwarding ops over the
  Unix socket, but the daemon-side handler now dispatches to a
  `fleet.FleetBackend` instead of `beads.BeadsBackend`.
- `internal/cli/ipc_issue_backend.go` — the client-side wrapper that
  decides IPC vs direct — simplified: drop `resolveFallbackBackend()`'s
  beads arm, keep the fleet arm.
- `internal/cli/daemon/daemon_cmd.go` — where the daemon wires up its
  IPC listener, swap the `IssueBackend` it hands to the listener from
  beads-backed to fleet-backed.
- Env vars: `BD_ACTOR` → `LOOM_AGENT_NAME`. The resolver reads
  `LOOM_AGENT_NAME` first, falls back to `BD_ACTOR` with a deprecation
  warning. Remove `BD_ACTOR` support one release later.
- `LOOM_BEADS_DIR` → `LOOM_WORKSPACE_DIR`. Same one-release alias
  treatment where it's read by `agent/agent_cmd.go`.
- Add `X-Loom-Agent-Name` header injection in the daemon's fleet call
  path when an op arrives via the agent socket.

### Commit 4 — `remove beads backend and vendored source`

The big deletion. Safe once commits 1–3 are in.

- `internal/backend/beads/` — deleted (beads IssueBackend impl)
- `internal/rpc/` — deleted (beads daemon wire protocol + client)
- `internal/types/` — deleted (beads daemon response types)
- `third_party/beads/` — deleted (vendored source)
- `.beads/` at repo root (`.beads/config.yaml`, `.beads/metadata.json`,
  `.beads/README.md`) — deleted
- `go.mod` — remove:
  ```
  require github.com/steveyegge/beads ...
  replace github.com/steveyegge/beads => ./third_party/beads
  ```
  Run `go mod tidy`.
- `internal/cli/fleetdb_server.go` → renamed `fleet_coordinator.go`,
  stripped of beads internals (see §3.4). Exported type renamed
  `FleetDBServer` → `FleetCoordinator`.
- All `_test.go` files that imported `internal/rpc`, `internal/types`,
  or the beads subtree — deleted or trimmed.

### Commit 5 — `collapse to fleet-only surface + docs`

Surface consolidation and user-facing cleanup.

- `internal/cli/issue_backend_resolve.go` — delete `IssueBackendBeads`,
  `IssueBackendFleetDB`, `IssueBackendAPI` constants; keep only
  `IssueBackendFleet`. Delete `LOOM_FLEETDB_ENABLED` handling. Delete
  `IsFleetActive` / `IsFleetDBActive` / `IsAPIActive` — replace with
  a single `fleet is active` (always true).
- `internal/backend/api/` — folded into `internal/backend/fleet/`.
  `AuthTransport`, OIDC device flow, transport plumbing preserved.
  Imports across the repo updated to `…/internal/backend/fleet`.
- `internal/cli/config/project.go` — drop `FleetDBSettings` field +
  type. Fix `IssueBackend` field comment.
- `internal/cli/config_validate.go` — error message lists only `fleet`.
- `internal/cli/root.go` — rewrite `Long` help; remove all `bd create`,
  `beads init`, `install-bd` references.
- `README.md` — rewrite Quick Start to use `loom issue create …`
  instead of `bd create …`. Drop the Credits section referencing
  beads + `third_party/beads`. Add a pointer to fleet server install
  docs.
- `AGENTS.md` — swap to the `loom issue …` surface drafted in commit 1.
- `Makefile` — remove `install-bd` target and its dependency in
  `install`.
- `.goreleaser.yml` — remove the `id: bd` build stanza and drop `bd`
  from archive `ids`.
- `scripts/install.sh` — remove the `bd` install/verify/PATH-conflict
  blocks; only install `loom`.
- `Dockerfile.dev` — remove the `bd` build step and the `bd` copy in
  the runtime image.
- `npm/package.json` — inspect; likely untouched.
- `docs/design/fleetdb-integration.md` — header updated to
  `**Status:** Superseded by docs/design/remove-beads.md`.
- `docs/arch/backend-infrastructure.md` — update sections referring
  to beads as a backend.

## 5. Non-goals / out of scope

- **Data migration from `.beads/` to fleet.** Explicit project
  decision: users re-create their issues in fleet. The
  `internal/cli/migrate/` package is removed rather than retargeted.
- **In-tree fleet-db storage.** No embedded miniredis-as-issue-store.
  Fleet is remote-only.
- **Offline mode.** `loom` cannot operate without a fleet server
  reachable. Release notes will call this out.
- **`loom mcp` MCP server for agents.** Agents use `loom issue …`
  shell commands. An MCP adapter is a follow-up, not part of this PR.
- **Backwards-compat shims.** No deprecation layer for
  `LOOM_FLEETDB_*` env vars, `daemon.fleetdb.*` config keys, or the
  `beads`/`fleetdb`/`api` backend names. Hard break.

## 6. Risks & breaking changes

| Risk | Mitigation |
|---|---|
| Every existing user's config breaks on upgrade | Prominent CHANGELOG entry; `loom doctor` detects old config keys and prints a migration note |
| No offline mode — requires fleet server | Release notes; fleet server install docs linked from every first-run error |
| Test blast radius ~40 files | Commits 1–2 land first so tests stabilize before the big deletion in commit 4 |
| IPC wire protocol change in commit 3 | Version bump on handshake; daemon rejects agent sockets with mismatched versions with a clear error |
| Agents already running when commit 4 lands | Expected: agents re-launch with new `loom` binary; `tmux` restart picks up the new surface |
| Config migration guidance | CHANGELOG + `loom doctor` hint. No automatic config rewrite. |

## 7. Open questions

- **Fleet server repo location.** README currently has no pointer to
  where users get the fleet server. Before merging commit 5, this
  needs a concrete URL.
- **OIDC auth bootstrap UX.** Today the device flow runs on first HTTP
  call; for agents, the daemon completes it once and they inherit.
  For a user running `loom issue show ABC-1` standalone (no daemon),
  the device flow has to run interactively — confirm that `httpclient`
  already handles this.
- **Doctor checks in fleet-only mode.** Current `loom doctor` is
  mostly beads-specific. What checks should survive?
  - Fleet URL set and reachable
  - Fleet workspace exists
  - Agent name resolvable
  - Auth token valid
  Add a new `checkFleetReachable` in commit 2.

## 8. File inventory at a glance

**Added (commit 1):**
- `internal/cli/issue/` — ~15 new command files + tests
- `docs/agents/AGENTS_FLEET.md` — draft agent instructions

**Renamed (commits 3–4):**
- `internal/cli/fleetdb_server.go` → `internal/cli/fleet_coordinator.go`
- `FleetDBServer` → `FleetCoordinator`
- `BD_ACTOR` → `LOOM_AGENT_NAME` (env)
- `LOOM_BEADS_DIR` → `LOOM_WORKSPACE_DIR` (env)
- `GetBeadsDir()` → `GetProjectDir()` (func)
- `.beads/` config tree → gone (no replacement)

**Deleted (commits 2–4):**
- `internal/backend/beads/` (8 files, ~5k LoC w/ tests)
- `internal/backend/api/` (merged into `fleet/`, then deleted)
- `internal/rpc/` (entire package)
- `internal/types/` (entire package)
- `internal/cli/cli_beads_adapter.go` + test
- `internal/cli/daemon_ensure.go` + test
- `internal/cli/ipc_issue_backend.go` (client wrapper, replaced by simplified version)
- `internal/cli/migrate/` (entire package)
- `internal/cli/fleetdb_server_test.go`
- `internal/cli/config_validate_fleetdb_test.go`
- `internal/cli/worktree_beadsdir_test.go`
- `internal/cli/fleet_mode_test.go` (edits, not full delete)
- `internal/webui/health_doctor.go` + any associated tests
- `third_party/beads/` (entire vendored tree)
- `.beads/` (repo-root config dir)

**Edited:**
- `internal/cli/deps.go` — drop BDRunner + fallback
- `internal/cli/issue_backend_resolve.go` — collapse to fleet-only
- `internal/cli/root.go` — help text
- `internal/cli/config/project.go` — drop FleetDBSettings
- `internal/cli/config_validate.go` — validator
- `internal/cli/doctor/doctor.go` + `doctor_checks.go` — drop bd checks, add fleet check
- `internal/cli/workspace/*.go` — drop bd init branches
- `internal/cli/serve/serve.go` — drop --no-daemon + stopIssueBackend
- `internal/cli/serve/workspacemgr/workspace.go` — drop initWorkspaceBeads
- `internal/cli/monitor/monitor_collect.go` — drop sync status
- `internal/cli/daemon/daemon_cmd.go` — construct backend from factory, not FleetDBServer
- `internal/backend/agentipc/backend.go` — daemon handler dispatches to fleet
- `README.md`, `AGENTS.md`
- `Makefile`, `.goreleaser.yml`, `scripts/install.sh`, `Dockerfile.dev`
- `docs/design/fleetdb-integration.md` — mark Superseded
- `docs/arch/backend-infrastructure.md` — reflect fleet-only
- `go.mod` + `go.sum` — drop beads require/replace; `go mod tidy`
- Test helpers across ~20 files — drop `MockBDRunner`, beads fixtures

## 9. Success criteria

- `rg -i '\bbd\b|\bbeads\b' --type go` returns zero hits outside of
  historical changelogs
- `go build ./...` and `go test ./...` green on every commit
- `loom init` in a fresh dir with no fleet URL fails fast with the
  documented error
- `loom issue ready` from inside an agent worktree returns live fleet
  data via the daemon socket
- `loom doctor` runs without calling `bd`
- Release tarball contains only `loom`, not `bd`
- `third_party/beads/` and `.beads/` no longer exist in the tree

---

*Prepared for review — not yet implemented. Approval required before
cutting the `remove-beads` branch.*
