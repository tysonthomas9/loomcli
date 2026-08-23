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
- Node.js/npm for the shell frontend (Node ≥ 22.18 — required by the Flue build)
- A Flue checkout at the pinned commit (`internal/workflows/FLUE_COMMIT`), located
  via `FLUE_REPO` (default `../flue`), built once with:
  `pnpm install && pnpm --filter @flue/runtime --filter @flue/cli build`
- Network access — or `LOOM_NODE_TARBALL` + `LOOM_NODE_SHASUMS` — for the pinned
  Node tarball (`internal/workflows/NODE_VERSION`), SHA-verified at build time

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
`loom local service` entrypoint discovers the bundled FleetDB sibling and web UI
resources, then sets `FLEET_DB_BIN` and `LOOM_FRONTEND_DIR` for the local
`loom serve` process.

## Embedded runtime

`prepare-sidecar.sh` also embeds a self-contained Flue runtime so the app runs the
built-in workflows with no dev checkout and no `node` on `PATH`:

- **Pinned Node** (`internal/workflows/NODE_VERSION`) → `Contents/MacOS/node`, plus
  its licence → `Contents/Resources/licenses/node-LICENSE`. Downloaded and
  SHA-verified from nodejs.org (or `LOOM_NODE_TARBALL`) by `prepare-node-runtime.sh`.
- **Both built-in artifacts** (`epic-runner`, `github-review-agent`) built from
  `FLUE_REPO` at the pin, packaged with `loom workflow package-builtin`
  (`--require-all` on the last), and staged into
  `Contents/Resources/builtin-workflows/` (each `dist/` with its nested
  `@loom/sdk`). Each `server.mjs` is load-smoked under the embedded `node`.
- **Digest bake:** the resulting `index_digest` is baked into the sidecar `loom`
  (`-ldflags -X …packaged.ExpectedIndexDigest=…`), which is what makes the build
  fail closed on a missing or tampered artifact.

Build-time escape hatches:

- `LOOM_SKIP_BUILTIN_ARTIFACTS=1` — embed Node only, no artifacts. The app is then
  **not** a packaged build and fails closed on desktop; useful for a quick shell build.
- `LOOM_BUILTIN_PREBUILT_DIST_DIR=<dir>` — reuse a prebuilt `<name>/dist/` instead
  of rebuilding it from Flue.

Prove a built app end to end (both built-ins register + run from embedded artifacts,
tamper fails closed) with the Definition-of-Done harness:

```sh
bash scripts/test-packaged-builtin-app.sh \
  "desktop/src-tauri/target/release/bundle/macos/Loom Agents.app"
# → PASS; observed output under desktop/dist-app-test/observed-output.txt
```

For the fast loop with no app build, `scripts/test-packaged-builtin-devbox.sh`
proves the same lane against a hand-laid tree.

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

For install, release packaging, verification, update, and troubleshooting
steps, see `docs/product/desktop-installation-runbook.md`.
