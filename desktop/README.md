# Loom Desktop

Tauri shell for the Loom macOS desktop app.

This package is intentionally a thin controller. The bundled `loom` sidecar owns
the local runtime:

- `loom local service`
- embedded FleetDB/miniredis
- Loom web/API server
- workspace daemon manager
- background agents

## Development

Prerequisites:

- Go toolchain for the sidecar
- Rust toolchain for Tauri
- Node.js/npm for the shell frontend

```sh
cd desktop
npm install
npm run dev
```

Build the macOS app bundle with:

```sh
npm run build
```

`npm run build:dmg` asks Tauri to create both the `.app` and `.dmg` bundles.
The DMG path is intentionally separate until signing, notarization, and CI
packaging behavior are finalized.

The Tauri config runs `scripts/prepare-sidecar.sh` before dev/build. That script
builds the web UI into `src-tauri/resources/webui`, builds `../cmd/loom` into
`src-tauri/binaries/loom-<target-triple>`, and, when the sibling FleetDB repo is
available, builds `src-tauri/binaries/fleet-db-<target-triple>`. The
`loom local service` entrypoint discovers the bundled FleetDB sibling and web UI
resources, then sets `FLEET_DB_BIN` and `LOOM_FRONTEND_DIR` for the local
`loom serve` process.

## Current Slice

The initial desktop shell can:

- build and bundle the `loom` sidecar
- call `loom local start`
- install and uninstall the app-owned macOS login service
- call `loom local status --json`
- call `loom local drain`, `resume`, and `stop`
- open local Loom workspace UI windows once healthy

Updater wiring, real drain enforcement, workspace daemon restoration, and
multi-window restoration are still tracked by
`docs/product/desktop-app-runtime-spec.md`.
