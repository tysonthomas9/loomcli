# Desktop App Runtime Spec

> **Status:** Partially implemented — the LaunchAgent service, the bundled
> sidecar, the app data directory, and the `loom local` command family shipped
> (`internal/cli/local/`). The tray/menu-bar status UI, the in-app updater, the
> `~/.loom` import path, "Install CLI", the diagnostics export, and hybrid
> worker mode did **not**. Each is marked inline. *audited 2026-07-24*

**Date:** 2026-05-06
**Related:** see [Related](#related) at the bottom.

## Naming

The shipped bundle is **`Loom Agents.app`** (`productName` in
`desktop/src-tauri/tauri.conf.json:3`), bundle identifier
`com.loom.agents.local`. The LaunchAgent label is the unrelated string
`com.loom.local` (`internal/cli/local/launchagent.go:15`). Earlier revisions of
this spec wrote `Loom.app` (no space, no "Agents") throughout; that path does
not exist and any command built from it fails.

## Purpose

Define how Loom ships as an installable macOS desktop app while preserving the
same fleet-db-backed local, cloud, and future hybrid execution model used by the
CLI and web product.

The desktop app should make local mode feel installed and persistent:

- background agents survive app window close, full app quit, logout/login, and
  machine reboot after the user logs in again
- users can open multiple workspace windows
- local data survives app updates
- the CLI can coexist with the app without silently switching data stores

## Product Position

The desktop app is a shell and controller, not a second Loom implementation.

| Layer | Owner | Product rule |
|---|---|---|
| Tauri shell | Windows, tray/menu, preferences, update UI, service controls | May store app preferences only. |
| Loom runtime | fleet-db, web/API server, daemon manager, agents, artifacts | Owns product state and execution. |
| fleet-db | Workspaces, repos, issues, agents, sessions, leases, commands | Remains the source of truth. |

Tauri must not store canonical tasks, sessions, agents, repos, leases,
artifacts, or workspace records. Those resources stay in fleet-db so local mode,
cloud mode, CLI mode, and future hybrid worker mode share one mental model.

## Target Architecture

```text
Loom Agents.app
  Tauri shell
    - multi-window workspace UI          [shipped]
    - tray/menu bar status               [NOT IMPLEMENTED]
    - update and diagnostics UI          [NOT IMPLEMENTED]
    - service install/start/stop controls[shipped]

  bundled loom sidecar
    - loom local service                 [shipped]
    - embedded fleet-db/miniredis        [shipped]
    - Loom web/API server                [shipped]
    - workspace daemon manager           [shipped]
    - background local agents            [shipped]
```

As built, the bundle carries **three** Mach-O binaries in `Contents/MacOS/`:
the Tauri shell `loom-desktop`, plus the two `externalBin` sidecars `loom` and
`fleet-db` (`desktop/src-tauri/tauri.conf.json:32`,
`desktop/scripts/release-macos.sh:119-120`). The web UI ships as a bundled
resource (`resources/webui/`), not as a served dev server.

There is no `TrayIcon` anywhere in `desktop/src-tauri/src/`. What exists is a
standard macOS menu bar with "New Workspace Window" items prepended to the File
and Window menus (`desktop/src-tauri/src/lib.rs:158-186`). Read "tray/menu bar
status" below as unbuilt.

The Tauri windows load the local Loom web UI from the shared local runtime:

```text
http://127.0.0.1:<port>/ws/ACME/kanban
http://127.0.0.1:<port>/ws/MOBILE/graph
http://127.0.0.1:<port>/ws/OPS/table
```

There is one local runtime per user, not one runtime per window.

## Background Service

Background agents must live in a per-user macOS `LaunchAgent`, not only inside
the foreground Tauri process.

```text
~/Library/LaunchAgents/com.loom.local.plist
  runs:
  /Applications/Loom Agents.app/Contents/MacOS/loom local service
```

The LaunchAgent starts after user login. This is intentional: local agents need
the user's filesystem permissions, git and SSH credentials, Keychain/session
context, and AI backend credentials.

The service owns:

- embedded fleet-db/miniredis startup and health
- local Loom web/API server startup and health
- workspace daemon manager
- agent process lifecycle
- runtime metadata, logs, and diagnostics
- draining and restart behavior during updates

The Tauri shell owns:

- installing, loading, unloading, and restarting the LaunchAgent
- rendering service status and health
- opening and restoring workspace windows
- asking the service to drain, stop, or resume agents
- surfacing diagnostics and update state

## Data Location

The macOS app uses an app-native data directory. As built:

```text
~/Library/Application Support/Loom/data/     <- the whole data dir
  runtime.json                               internal/cli/local/runtime.go:27,142
  state.json                                 internal/bootstrap/paths.go:56-62
  logs/
    loom-serve.log                           internal/cli/local/runtime.go:146
    loom-local-service.log                   internal/cli/local/runtime.go:207
    loom-daemon.log                          internal/cli/local/runtime.go:211
  fleet-db/
    redis-snapshot.json                      internal/bootstrap/embedded.go:305
  workspaces/<name>/                         internal/bootstrap/paths.go:64-73
```

`loom local` itself only creates the data dir and `logs/`
(`internal/cli/local/runtime.go:133-136`); the rest appear when their owners
first write. The `config/` and `diagnostics/` directories in earlier revisions
of this spec were never created — cut.

Data-dir resolution is a three-step ladder, highest first
(`internal/cli/local/runtime.go:106-128`):

1. `LOOM_DESKTOP_DATA_DIR`
2. `LOOM_CONFIG_DIR`
3. `~/Library/Application Support/Loom/data`

The LaunchAgent plist sets both env vars explicitly
(`internal/cli/local/launchagent.go:66-72`) so the service and the bundled CLI
agree. Note that `LOOM_CONFIG_DIR` is also what `bootstrap.LoomDir()` reads
(`internal/bootstrap/paths.go:39-44`), which is why setting it redirects
`state.json` and `workspaces/` too.

Product state must never be written inside `Loom Agents.app`, because app updates
replace the bundle. Existing CLI users may still have data in `~/.loom`; the
desktop app should offer an explicit migration/import path instead of silently
moving or merging state.

## CLI Coexistence

Users may install Loom through the macOS app, through the standalone CLI
installer, or both. The product must make the active binary and data directory
clear.

Recommended behavior:

| Install shape | Default data dir | Expected behavior |
|---|---|---|
| App-installed CLI | `~/Library/Application Support/Loom/data` | Controls the LaunchAgent-backed local runtime. |
| Standalone CLI | `~/.loom` | Works independently unless `LOOM_CONFIG_DIR` points to app data. |
| Cloud CLI | N/A local runtime | Uses `LOOM_FLEET_DB_URL` and does not start local fleet-db. |

The app may offer "Install CLI" by creating a stable wrapper or symlink:

```text
/usr/local/bin/loom -> /Applications/Loom Agents.app/Contents/MacOS/loom
```

If a standalone `loom` already exists earlier on `PATH`, the app should warn
instead of replacing it without consent.

**Not implemented.** No symlink/wrapper install exists in `internal/cli/local/`
or `desktop/src-tauri/src/`. Today the user invokes the bundled binary by full
path (see [Service Commands](#service-commands)).

`loom doctor` should report:

- binary path and version
- app bundle version when applicable
- active `LOOM_CONFIG_DIR`
- local service status
- fleet-db URL and health
- selected local/cloud mode
- whether the CLI is app-bundled or standalone

**Not implemented — `loom doctor` reports none of the above.** It exists
(`internal/cli/doctor/doctor.go:71`) but its checks are a different set, named:
`git`, `git_repo`, `tmux`, `backend_cli`, `project_config`, `global_config`,
`worktrees`, `redis`, `fleetdb`, `fleet`, `issue_backend`, `loom_daemon`,
`daemon_stuck`, `stale_locks`, `stale_sessions`, `stale_signal_files`,
`orphaned_tmux_sessions`, `orphaned_transcripts`, `orphaned_fleet_locks`
(`internal/cli/doctor/doctor_checks.go`,
`internal/cli/doctor/doctor_fleetdb.go`, `internal/cli/doctor/doctor_checks_*.go`). Of the list above, only fleet-db health is covered
(check `fleetdb`). For desktop-runtime state use `loom local status --json`
instead — it reports PID, serve PID, data dir, URL, port, executable, binary
hash, build, and `healthy` (`internal/cli/local/runtime.go:32-52`).

## Runtime Modes

The desktop app should support local mode first and keep cloud/hybrid mode as
explicit mode choices.

| Mode | Runtime behavior |
|---|---|
| Local | Start app-managed LaunchAgent, embedded fleet-db/miniredis, local server, and local agents. |
| Cloud | Do not start embedded fleet-db. Connect UI/CLI to configured cloud fleet-db/API. |
| Hybrid worker | Connect to cloud fleet-db/API while this Mac contributes local agents as worker capacity. |

Mode selection must be explicit. Selecting a cloud workspace must not
implicitly start local fleet-db, and selecting local mode must not mutate cloud
state unless the user has configured a hybrid worker.

**As built:** Local and Cloud are real and are the *only* two modes —
`bootstrap.DetectMode` returns Cloud when `LOOM_FLEET_DB_URL` is set and Local
otherwise (`internal/bootstrap/mode.go:51-58`), and
`scripts/check-control-plane-paths.sh` fails the gate if a third `Mode*`
constant appears. **Hybrid worker mode does not exist** as a desktop mode. Two
adjacent things do, and neither is app-selectable as described here:

- `loom worker --control-plane <url>` — a process that contributes worker
  capacity to a remote control plane (`internal/cli/serve/worker/worker_cmd.go:40-50`).
- A default agent runtime of `local` or `daytona`, persisted in the desktop
  settings file and editable over HTTP (`internal/localsettings/settings.go:27-28`,
  `internal/webui/handlers/localsettings/localsettings.go:189-198`). It is a
  **single machine-local value** — `AgentRuntimeConfig` holds one `Default`
  field and the file is keyed by data dir, not by workspace
  (`internal/localsettings/settings.go:57-60,121`). This routes *task runs* to
  a remote sandbox; it does not make the Mac a worker.

## Multi-Workspace Windows

The app must allow multiple workspace windows backed by the same runtime.

Expected behaviors:

- "New Workspace Window" opens another Tauri window. *(Shipped — a "New
  Workspace Window" item is prepended to both the File and Window menus,
  `desktop/src-tauri/src/lib.rs:160-186`; workspace windows are built by
  `WebviewWindowBuilder` against the local runtime URL, `lib.rs:425,447`.)*
- "Open Workspace in New Window" opens the selected workspace route.
- Closing one window does not stop the runtime or agents.
- Quitting the app does not stop agents if the LaunchAgent is installed and
  enabled.
- Reopening the app restores the last selected workspace windows when possible.
- If the local runtime is unhealthy, all windows show the same reconnect or
  restart state.

Workspace daemon ownership is scoped to workspace records and agent
definitions, not to windows. A window is only a view into a shared runtime.

## Updates

Updates happen at the app bundle level. A single app update includes:

- Tauri shell
- embedded web UI
- bundled `loom` sidecar
- embedded fleet-db/local runtime code

Local data remains in the app data directory and survives bundle replacement.

The update flow must coordinate with the LaunchAgent:

1. Download update in the background.
2. Mark local service as draining.
3. Stop new task claims.
4. Let active agents finish, or time out according to policy.
5. Flush fleet-db/miniredis snapshot and runtime metadata.
6. Stop the LaunchAgent.
7. Replace `Loom Agents.app`.
8. Restart the LaunchAgent.
9. Resume claims.
10. Restore windows.

The app must not hot-swap the sidecar while agents are running. If agents are
active, the UI should offer:

- install now after draining
- install after agents finish
- remind me later

Cloud mode may update the shell and UI without draining a local fleet-db runtime,
but it still needs a client/server compatibility check.

**Not implemented.** There is no in-app updater, no update channel, and no
drain-before-replace orchestration. The shipped release path is manual: build a
signed/notarized DMG and drag-replace the bundle. See
[`desktop-installation-runbook.md`](desktop-installation-runbook.md), which also
records the macOS App Management gotcha that makes scripted overwrite fail. The
sequence above remains the target design.

## Service Commands

**Shipped**, with a different shape than proposed. The full command set
registered by `internal/cli/local/local_cmd.go:128` is:

```text
loom local service           # long-running LaunchAgent entrypoint (foreground)
loom local start             # start the runtime service in the background
loom local status [--json]   # local runtime status; --json includes "healthy"
loom local stop              # stop the runtime service
loom local restart           # stop, wait 500ms, start (local_cmd.go:78-85)
loom local install-service   # write + bootstrap ~/Library/LaunchAgents/com.loom.local.plist
loom local uninstall-service # bootout + remove the plist
loom local drain             # mark the runtime draining (pause claims)
loom local resume            # resume claims
loom local logs              # print local runtime log paths
```

All take a `--data-dir` persistent flag; `service`, `start` and
`install-service` take `--port`, and `service` also takes `--bind`
(`internal/cli/local/local_cmd.go:122-127`).

Differences from the proposal: `start`, `install-service` and
`uninstall-service` exist and were not listed; `loom local stop` does not drain
first (`drain` is a separate explicit step); and `loom doctor` is a
general-purpose command, not a desktop diagnostic — see
[CLI Coexistence](#cli-coexistence).

These commands should be idempotent and safe to call repeatedly from the app.

## Security Requirements

Local agents are powerful local code execution. The desktop app must make that
clear and keep the local API constrained.

Required protections, with what is actually in place:

- **Bind local HTTP APIs to `127.0.0.1` by default.** *Shipped.* `loom serve`
  defaults `LOOM_BIND_ADDR` to `127.0.0.1` (`internal/cli/serve/serve.go:160`)
  and logs a warning on any non-loopback bind (`warnNonLocalBind`,
  `internal/cli/serve/serve.go:570-573`). `loom local service` defaults its
  `--bind` flag to `127.0.0.1` too (`internal/cli/local/local_cmd.go:124`).
- **Add origin and CSRF protections for localhost endpoints.** *Neither, in the
  desktop default.* There is **no CSRF token middleware** anywhere under
  `internal/webui`. The CORS middleware does reject cross-origin non-preflight
  requests (403 unless same-origin or allow-listed,
  `internal/webui/server/middleware/cors.go:61-74`) and it sits in the global
  chain, not on `/api/` only (`internal/webui/app/server.go:268`) — **but it is
  a pure passthrough unless `CORSConfig.Enabled`**, and `Enabled` is set only
  when `--cors`/`LOOM_CORS_ORIGIN` or an external-auth origin supplies at least
  one origin (`internal/cli/serve/serve.go:740-742`,
  `internal/webui/app/server_app.go:59-74`). A default `loom local service`
  supplies none, so no origin check runs. Ignore `CORSConfig.RejectDisallowed`
  (`cors.go:13`): it is dead — never read by the middleware and never set
  anywhere in non-test code, and its "always active in internal/webui" comment
  is stale.
- **Scope Tauri sidecar permissions to the minimum needed.** *Partially.*
  Capabilities are declared in `desktop/src-tauri/capabilities/{default,workspace}.json`;
  whether they are minimal is UNVERIFIED.
- Show which repos and workspaces agents can access. — UNVERIFIED
- Require user intent before installing or replacing the CLI. — moot; no CLI
  install flow exists.
- **Require user intent before enabling background agents at login.**
  *Effectively yes:* the LaunchAgent is only written when someone runs
  `loom local install-service` (`internal/cli/local/launchagent.go:79-113`);
  nothing installs it implicitly.
- Keep secrets out of Tauri store and logs. — UNVERIFIED. Note that desktop
  runtime credentials are sealed at rest rather than stored plaintext
  (`localsettings.SealRuntimeCredential`, `internal/localsettings/settings.go`).

The product should avoid a system-wide LaunchDaemon. A per-user LaunchAgent is
the correct default because it runs with the user's credentials and permissions.
*This held:* the only plist Loom writes is under `~/Library/LaunchAgents/`
(`internal/cli/local/launchagent.go:178-184`).

Two behaviors a security-conscious reader should know about, documented here
because they are not obvious from the app: the daemon re-execs the `loom`
binary and sets process groups for supervised agents
(`internal/cli/daemon/supervisor/spawn.go`), and agent backends are launched
with their own sandboxes disabled — `codex` with
`--dangerously-bypass-approvals-and-sandbox`
(`internal/cli/backends/backend_codex.go`) and the claude lead runtime with
`--dangerously-skip-permissions`
(`internal/cli/backends/harness_lead_runtime.go`). Background agents therefore
run with the full authority of the logged-in user.

## Diagnostics

The app should include a "Copy Diagnostics" action that gathers:

- app version and sidecar version
- local service PID, uptime, and port
- LaunchAgent load state
- active data directory
- fleet-db health and runtime URL
- workspace daemon status
- running agents and active sessions
- recent service logs
- recent update attempts
- cloud URL and compatibility state when cloud mode is selected

Diagnostics must redact API keys, auth tokens, and backend credentials.

**Not implemented** — there is no "Copy Diagnostics" action. The manual
equivalent today is `loom local status --json`, `loom local logs`, and
`launchctl print gui/$(id -u)/com.loom.local`; see the Troubleshooting section
of [`desktop-installation-runbook.md`](desktop-installation-runbook.md).

## Acceptance Criteria

- Installing `Loom Agents.app` can enable a per-user LaunchAgent.
- Background agents continue after all Tauri windows close.
- Background agents restart after reboot once the user logs in.
- Multiple workspace windows can be open concurrently.
- The local runtime is shared across windows.
- Updating the app drains or pauses agents before replacing the sidecar.
- Local product state survives app update.
- The app-installed CLI and standalone CLI can coexist without silent data
  directory confusion.
- Cloud mode can connect without starting embedded fleet-db.
- fleet-db remains the only source of truth for workspaces, issues, agents,
  sessions, leases, commands, and artifacts.

## Open Questions

- Should background agents be enabled by default on first launch, or should the
  user opt in during onboarding?
- What is the default drain timeout during app update?
- Should app data import from `~/.loom` be one-way copy, move, or explicit
  "use existing CLI data" mode?
- Should "Install CLI" target `/usr/local/bin`, `~/.local/bin`, or offer both?
- Which update channel model is needed first: stable only, or stable/beta?

All five remain open — none of the features they concern were built.
`desktop-installation-runbook.md` tracks the same gaps from the release side.

## Related

- [`desktop-installation-runbook.md`](desktop-installation-runbook.md) — build,
  sign, notarize, install, verify, troubleshoot. The operational companion to
  this spec, and the more accurate of the two on packaging.
- [`local-mode-product-spec.md`](local-mode-product-spec.md) — the topology this
  app packages, and the CI-enforced fleet-db-only invariant.
- [`daemon-agent-runtime-architecture.md`](daemon-agent-runtime-architecture.md)
  — what the bundled `loom daemon` does once the service starts it.
- [`web-onboarding-spec.md`](web-onboarding-spec.md) — first-run flow inside the
  window.
- [`agent-run-ux-spec.md`](agent-run-ux-spec.md) and
  [`session-artifact-contract.md`](session-artifact-contract.md) — what the
  window shows.
- `desktop/README.md` — the Tauri package's own README.
- `docs/security.md` — auth modes, IPC and subprocess env policy.
- `docs/loom-glossary.md` — "local mode" vs "fleet mode", and the three senses
  of "backend".
