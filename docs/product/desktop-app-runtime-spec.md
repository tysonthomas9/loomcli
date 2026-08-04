# Desktop App Runtime Spec

**Status:** Draft
**Date:** 2026-05-06
**Related:** `docs/product/local-mode-product-spec.md`,
`docs/product/agent-run-ux-spec.md`,
`docs/product/session-artifact-contract.md`

## Purpose

Define how Loom ships as an installable macOS desktop app while preserving the
same FleetDB-backed local, cloud, and future hybrid execution model used by the
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
| Loom runtime | FleetDB, web/API server, daemon manager, agents, artifacts | Owns product state and execution. |
| FleetDB | Workspaces, repos, issues, agents, sessions, leases, commands | Remains the source of truth. |

Tauri must not store canonical tasks, sessions, agents, repos, leases,
artifacts, or workspace records. Those resources stay in FleetDB so local mode,
cloud mode, CLI mode, and future hybrid worker mode share one mental model.

## Target Architecture

```text
Loom.app
  Tauri shell
    - multi-window workspace UI
    - tray/menu bar status
    - update and diagnostics UI
    - service install/start/stop controls

  bundled loom sidecar
    - loom local service
    - embedded FleetDB/miniredis
    - Loom web/API server
    - workspace daemon manager
    - background local agents
```

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
  /Applications/Loom.app/Contents/MacOS/loom local service
```

The LaunchAgent starts after user login. This is intentional: local agents need
the user's filesystem permissions, git and SSH credentials, Keychain/session
context, and AI backend credentials.

The service owns:

- embedded FleetDB/miniredis startup and health
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

The macOS app should use an app-native data directory:

```text
~/Library/Application Support/Loom/
  data/
    fleet-db/
    runtime.json
    workspaces/
    logs/
  config/
  diagnostics/
```

The app-managed runtime must set:

```text
LOOM_CONFIG_DIR="$HOME/Library/Application Support/Loom/data"
```

Product state must never be written inside `Loom.app`, because app updates
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
| Cloud CLI | N/A local runtime | Uses `LOOM_FLEET_DB_URL` and does not start local FleetDB. |

The app may offer "Install CLI" by creating a stable wrapper or symlink:

```text
/usr/local/bin/loom -> /Applications/Loom.app/Contents/MacOS/loom
```

If a standalone `loom` already exists earlier on `PATH`, the app should warn
instead of replacing it without consent.

`loom doctor` should report:

- binary path and version
- app bundle version when applicable
- active `LOOM_CONFIG_DIR`
- local service status
- FleetDB URL and health
- selected local/cloud mode
- whether the CLI is app-bundled or standalone

## Runtime Modes

The desktop app should support local mode first and keep cloud/hybrid mode as
explicit mode choices.

| Mode | Runtime behavior |
|---|---|
| Local | Start app-managed LaunchAgent, embedded FleetDB/miniredis, local server, and local agents. |
| Cloud | Do not start embedded FleetDB. Connect UI/CLI to configured cloud FleetDB/API. |
| Hybrid worker | Connect to cloud FleetDB/API while this Mac contributes local agents as worker capacity. |

Mode selection must be explicit. Selecting a cloud workspace must not
implicitly start local FleetDB, and selecting local mode must not mutate cloud
state unless the user has configured a hybrid worker.

## Multi-Workspace Windows

The app must allow multiple workspace windows backed by the same runtime.

Expected behaviors:

- "New Workspace Window" opens another Tauri window.
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
- embedded FleetDB/local runtime code

Local data remains in the app data directory and survives bundle replacement.

The update flow must coordinate with the LaunchAgent:

1. Download update in the background.
2. Mark local service as draining.
3. Stop new task claims.
4. Let active agents finish, or time out according to policy.
5. Flush FleetDB/miniredis snapshot and runtime metadata.
6. Stop the LaunchAgent.
7. Replace `Loom.app`.
8. Restart the LaunchAgent.
9. Resume claims.
10. Restore windows.

The app must not hot-swap the sidecar while agents are running. If agents are
active, the UI should offer:

- install now after draining
- install after agents finish
- remind me later

Cloud mode may update the shell and UI without draining a local FleetDB runtime,
but it still needs a client/server compatibility check.

## Service Commands

The bundled CLI should expose service-oriented commands for Tauri and advanced
users:

```text
loom local service        # long-running LaunchAgent entrypoint
loom local status         # local runtime health and port
loom local stop           # stop local runtime after draining
loom local restart        # restart local runtime
loom local drain          # stop new claims, let active agents finish
loom local resume         # resume claims
loom local logs           # show or locate service logs
loom doctor               # diagnostics across app/CLI/service/FleetDB
```

These commands should be idempotent and safe to call repeatedly from the app.

## Security Requirements

Local agents are powerful local code execution. The desktop app must make that
clear and keep the local API constrained.

Required protections:

- Bind local HTTP APIs to `127.0.0.1` by default.
- Add origin and CSRF protections for localhost endpoints.
- Scope Tauri sidecar permissions to the minimum service commands needed by
  the app.
- Show which repos and workspaces agents can access.
- Require user intent before installing or replacing the CLI.
- Require user intent before enabling background agents at login.
- Keep secrets out of Tauri store and logs.

The product should avoid a system-wide LaunchDaemon. A per-user LaunchAgent is
the correct default because it runs with the user's credentials and permissions.

## Diagnostics

The app should include a "Copy Diagnostics" action that gathers:

- app version and sidecar version
- local service PID, uptime, and port
- LaunchAgent load state
- active data directory
- FleetDB health and runtime URL
- workspace daemon status
- running agents and active sessions
- recent service logs
- recent update attempts
- cloud URL and compatibility state when cloud mode is selected

Diagnostics must redact API keys, auth tokens, and backend credentials.

## Acceptance Criteria

- Installing `Loom.app` can enable a per-user LaunchAgent.
- Background agents continue after all Tauri windows close.
- Background agents restart after reboot once the user logs in.
- Multiple workspace windows can be open concurrently.
- The local runtime is shared across windows.
- Updating the app drains or pauses agents before replacing the sidecar.
- Local product state survives app update.
- The app-installed CLI and standalone CLI can coexist without silent data
  directory confusion.
- Cloud mode can connect without starting embedded FleetDB.
- FleetDB remains the only source of truth for workspaces, issues, agents,
  sessions, leases, commands, and artifacts.

## Open Questions

- Should background agents be enabled by default on first launch, or should the
  user opt in during onboarding?
- What is the default drain timeout during app update?
- Should app data import from `~/.loom` be one-way copy, move, or explicit
  "use existing CLI data" mode?
- Should "Install CLI" target `/usr/local/bin`, `~/.local/bin`, or offer both?
- Which update channel model is needed first: stable only, or stable/beta?
