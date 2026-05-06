# Desktop App Installation Runbook

**Status:** Draft
**Date:** 2026-05-06
**Related:** `docs/product/desktop-app-runtime-spec.md`,
`docs/product/local-mode-product-spec.md`,
`desktop/README.md`

This runbook documents how to build, install, verify, troubleshoot, and update
the macOS Tauri app during development and early release packaging.

The desktop app is a shell around the same local runtime used by the CLI:

- Tauri owns app windows, menus, and service controls.
- The bundled `loom` sidecar owns `loom local service`.
- The local service owns embedded FleetDB/miniredis, `loom serve`, workspace
  daemons, and background agents.
- FleetDB remains the source of truth for workspaces, repos, issues, agents,
  sessions, leases, terminal sessions, commands, and artifacts.

## Build Prerequisites

Install these tools on the build machine:

- Go toolchain for the `loom` and FleetDB sidecars.
- Rust toolchain for Tauri.
- Node.js and npm for the Tauri shell and bundled web UI.
- A sibling FleetDB checkout at `../fleet-db`, or set `FLEET_DB_REPO` to the
  FleetDB repo path before building.

From the `loomcli` repo root, install JavaScript dependencies once:

```sh
npm --prefix desktop install
npm --prefix internal/webui/frontend install
```

## Development Run

Start the Tauri app in development mode:

```sh
cd desktop
npm run dev
```

`npm run dev` runs `scripts/prepare-sidecar.sh` first. That script:

- builds the web UI into `desktop/src-tauri/resources/webui`
- builds `cmd/loom` into `desktop/src-tauri/binaries/loom-<target-triple>`
- builds FleetDB into
  `desktop/src-tauri/binaries/fleet-db-<target-triple>` when FleetDB is
  available

If FleetDB is not in `../fleet-db`, run:

```sh
FLEET_DB_REPO=/path/to/fleet-db npm run dev
```

## Local App Bundle

Build a local `.app` bundle:

```sh
npm --prefix desktop run build
```

From the repo root, open the built app with:

```sh
open -n desktop/src-tauri/target/release/bundle/macos/Loom.app
```

From inside `desktop/`, the same path is:

```sh
open -n src-tauri/target/release/bundle/macos/Loom.app
```

Build a local `.app` and `.dmg`:

```sh
npm --prefix desktop run build:dmg
```

The generated artifacts are under:

```text
desktop/src-tauri/target/release/bundle/macos/
desktop/src-tauri/target/release/bundle/dmg/
```

The current DMG is a development packaging artifact. Signing, notarization,
release-channel metadata, and automatic update publication are still separate
release tasks.

## Manual Installation

For local manual testing:

1. Build with `npm --prefix desktop run build`.
2. Copy or drag `desktop/src-tauri/target/release/bundle/macos/Loom.app` into
   `/Applications`.
3. Open `/Applications/Loom.app`.
4. Let the app start the local runtime.
5. Open a workspace window from the app once the runtime is healthy.

For DMG testing:

1. Build with `npm --prefix desktop run build:dmg`.
2. Open the DMG under `desktop/src-tauri/target/release/bundle/dmg/`.
3. Drag `Loom.app` into `/Applications`.
4. Launch `/Applications/Loom.app`.

If macOS blocks the unsigned build, use a locally signed/notarized build for
release testing. Avoid documenting a bypass as the supported install path.

## App Data

The app-managed runtime uses an app-native data directory:

```text
~/Library/Application Support/Loom/data
```

The app and LaunchAgent set:

```text
LOOM_CONFIG_DIR="$HOME/Library/Application Support/Loom/data"
LOOM_DESKTOP_DATA_DIR="$HOME/Library/Application Support/Loom/data"
```

Product data must not be stored inside `Loom.app` because app updates replace
the bundle. A standalone CLI install may still use `~/.loom`; do not silently
merge or move that data into the app directory.

## Local Service

The desktop runtime can be controlled through the bundled CLI. When testing an
installed app, use:

```sh
/Applications/Loom.app/Contents/MacOS/loom local status --json
/Applications/Loom.app/Contents/MacOS/loom local logs
/Applications/Loom.app/Contents/MacOS/loom local drain
/Applications/Loom.app/Contents/MacOS/loom local resume
/Applications/Loom.app/Contents/MacOS/loom local stop
```

The persistent service is a per-user LaunchAgent:

```text
~/Library/LaunchAgents/com.loom.local.plist
```

Install or replace it:

```sh
/Applications/Loom.app/Contents/MacOS/loom local install-service
```

Remove it:

```sh
/Applications/Loom.app/Contents/MacOS/loom local uninstall-service
```

The LaunchAgent runs `loom local service`, which starts `loom serve` and the
workspace daemon manager. Background agents should survive closing all app
windows and restart after user login.

## Verification

Run these checks after a local app build:

```sh
npm --prefix desktop run build
open -n desktop/src-tauri/target/release/bundle/macos/Loom.app
```

Then verify:

- The app window becomes visible and loads the workspace UI after runtime
  health.
- `loom local status --json` reports `healthy: true`.
- Creating a workspace works.
- Adding a repo works.
- Adding an agent works.
- Assigning work starts or queues an agent session.
- The lead terminal renders scrollback and accepts input.
- Closing the window does not stop the local runtime.
- Reopening the app restores or opens a workspace window.
- Opening another workspace window does not start a second runtime.

Useful status commands:

```sh
/Applications/Loom.app/Contents/MacOS/loom local status
/Applications/Loom.app/Contents/MacOS/loom local logs
launchctl print gui/$(id -u)/com.loom.local
```

## Updates

The release update flow must treat the app bundle and sidecars as one unit:

1. Download the app update.
2. Mark the local runtime as draining.
3. Stop new task claims.
4. Let active agents finish, or stop them according to the release policy.
5. Flush FleetDB/miniredis and runtime metadata.
6. Stop the LaunchAgent.
7. Replace `Loom.app`.
8. Restart the LaunchAgent.
9. Resume claims.
10. Restore workspace windows.

Do not hot-swap the bundled `loom` or FleetDB sidecar while agents are
running. If a user also has a standalone CLI installed, the app update must not
rewrite that CLI without explicit user consent.

## Troubleshooting

If the app opens but the workspace never loads, check:

```sh
/Applications/Loom.app/Contents/MacOS/loom local status --json
/Applications/Loom.app/Contents/MacOS/loom local logs
```

If the app bundle path is missing after build, confirm the command was run from
the expected directory:

```sh
ls desktop/src-tauri/target/release/bundle/macos/Loom.app
```

If FleetDB is not bundled, make sure the sibling repo exists or set
`FLEET_DB_REPO`:

```sh
FLEET_DB_REPO=/path/to/fleet-db npm --prefix desktop run build
```

If the LaunchAgent is stale after moving the app, reinstall it:

```sh
/Applications/Loom.app/Contents/MacOS/loom local uninstall-service
/Applications/Loom.app/Contents/MacOS/loom local install-service
```

If the local runtime appears stuck, drain and restart it:

```sh
/Applications/Loom.app/Contents/MacOS/loom local drain
/Applications/Loom.app/Contents/MacOS/loom local stop
/Applications/Loom.app/Contents/MacOS/loom local start
/Applications/Loom.app/Contents/MacOS/loom local resume
```

Keep service logs and `runtime.json` with bug reports, but redact secrets,
tokens, API keys, and repo credentials before sharing diagnostics.

## Release Gaps

The current repository can build and manually install a development `.app`.
Before shipping to users, finish:

- code signing and notarization
- signed DMG packaging
- update metadata generation and hosting
- updater UI and rollback behavior
- first-run background service consent
- app data migration/import from standalone CLI data
- CLI install/link flow and PATH conflict warnings
- diagnostics export with secret redaction
- full visual regression coverage for desktop workflows
