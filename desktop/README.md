# Superfactory Desktop

Tauri shell for the Superfactory macOS desktop app.

This package is intentionally a thin controller. The bundled `loom` sidecar owns
the local runtime:

- `loom local service`
- embedded FleetDB/miniredis
- Superfactory web/API UI served by the Loom runtime
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

After changing bundled Go sidecar code, rebuild and relaunch the packaged app
with:

```sh
npm run refresh:app
```

For web UI-only changes, use the faster `npm run refresh:webui`.

`npm run build:dmg` asks Tauri to create both the `.app` and `.dmg` bundles,
unsigned (ad-hoc) — fine for local testing but blocked by Gatekeeper on other
Macs.

## Signed / notarized release

To produce a Developer ID–signed, notarized, and stapled DMG that launches on
other Macs:

```sh
# one-time: store notarization creds in a keychain profile
xcrun notarytool store-credentials <profile> --apple-id <you> --team-id BN879H59CY --password <app-pw>

# build + sign + notarize + staple + verify
NOTARY_PROFILE=<profile> npm run release:macos
```

The verified DMG lands in `desktop/dist-release/`, and the script prints a
`gh release create` command to upload it. CI does the same on a `desktop-v*` tag
via `.github/workflows/desktop-release.yml`. Full runbook (required secrets, cert
export, troubleshooting): `docs/product/desktop-installation-runbook.md`
(§ Signed Release Build).

The Tauri config runs `scripts/prepare-sidecar.sh` before dev/build. That script
builds the web UI into `src-tauri/resources/webui`, builds `../cmd/loom` into
`src-tauri/binaries/loom-<target-triple>`, and, when the sibling FleetDB repo is
available, builds `src-tauri/binaries/fleet-db-<target-triple>`. The
FleetDB path must be a working checkout with the connector and role-kind APIs
used by this Loom branch; the build fails early when `FLEET_DB_REPO` points at
a bare worktree anchor or an incompatible checkout. The
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
- open local Superfactory workspace UI windows once healthy

Updater wiring, real drain enforcement, workspace daemon restoration, and
multi-window restoration are still tracked by
`docs/product/desktop-app-runtime-spec.md`.

For install, release packaging, verification, update, and troubleshooting
steps, see `docs/product/desktop-installation-runbook.md`.
