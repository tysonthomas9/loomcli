# Loom Desktop

> **Status:** Partially implemented — the shell, sidecar build, signed-release
> path, and `loom local` control surface are shipped
> (`internal/cli/local/local_cmd.go:40-116`, `desktop/scripts/release-macos.sh`).
> Updater wiring, real drain enforcement, workspace-daemon restoration, and
> multi-window restoration are not built; see § Current Slice.
> *audited 2026-07-24*

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
`src-tauri/binaries/loom-<target-triple>`, and, when the sibling fleet-db repo is
available, builds `src-tauri/binaries/fleet-db-<target-triple>`. The
`loom local service` entrypoint discovers the bundled fleet-db sibling and web UI
resources, then sets `FLEET_DB_BIN` and `LOOM_FRONTEND_DIR` for the local
`loom serve` process.

The fleet-db checkout is looked up at `<loomcli repo>/../fleet-db` and is
overridable with `FLEET_DB_REPO` (`desktop/scripts/prepare-sidecar.sh:8`).
Missing, the script only warns: the build succeeds without a bundled fleet-db
and the local runtime then needs `FLEET_DB_BIN` supplied some other way
(`desktop/scripts/prepare-sidecar.sh:68`).

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

## Related

- [`../docs/product/desktop-installation-runbook.md`](../docs/product/desktop-installation-runbook.md)
  — install, release packaging, verification, update, troubleshooting
  (§ Signed Release Build covers the notarized DMG)
- [`../docs/product/desktop-app-runtime-spec.md`](../docs/product/desktop-app-runtime-spec.md)
  — the spec that owns the unbuilt slices above
- [`../README.md`](../README.md) — the `loom` CLI the sidecar is built from
- [`../deploy/README.md`](../deploy/README.md) — the multi-host packaging of the
  same server
