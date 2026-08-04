# Desktop App Installation Runbook

> **Status:** Current — follow it. Every command, path, script, secret name and
> make/npm target below was re-verified against the tree. The one systematic
> error, `Loom.app` for the real bundle name `Loom Agents.app`, is fixed
> throughout. The "Release Gaps" list at the bottom is still accurate.
> *audited 2026-07-24*

**Date:** 2026-05-06 · last substantive update 2026-06-22
**Related:** see [Related](#related) at the bottom.

This runbook documents how to build, install, verify, troubleshoot, and update
the macOS Tauri app during development and early release packaging.

The app bundle is named **`Loom Agents.app`** (`productName` in
`desktop/src-tauri/tauri.conf.json:3`) — note the space. Quote it in shell
commands. Its `Contents/MacOS/` holds three binaries: the Tauri shell
`loom-desktop` and the two sidecars `loom` and `fleet-db`
(`desktop/src-tauri/tauri.conf.json:32`).

The desktop app is a shell around the same local runtime used by the CLI:

- Tauri owns app windows, menus, and service controls.
- The bundled `loom` sidecar owns `loom local service`.
- The local service owns embedded fleet-db/miniredis, `loom serve`, workspace
  daemons, and background agents (`internal/cli/local/local_cmd.go:241-249`,
  `internal/cli/local/daemon.go:29-33`).
- fleet-db remains the source of truth for workspaces, repos, issues, agents,
  sessions, leases, terminal sessions, commands, and artifacts.

## Build Prerequisites

Install these tools on the build machine:

- Go toolchain for the `loom` and fleet-db sidecars.
- Rust toolchain for Tauri.
- Node.js and npm for the Tauri shell and bundled web UI.
- A sibling fleet-db checkout at `../fleet-db`, or set `FLEET_DB_REPO` to the
  fleet-db repo path before building.

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

`npm run dev` is `tauri dev` (`desktop/package.json:10`). Tauri's
`beforeDevCommand` runs `npm run prepare-sidecar && npm run dev:frontend`
(`desktop/src-tauri/tauri.conf.json:7`), so `scripts/prepare-sidecar.sh` runs
first either way. `npm run build` gets the same treatment via
`beforeBuildCommand` (`desktop/src-tauri/tauri.conf.json:8`).

`scripts/prepare-sidecar.sh`:

- requires `rustc` on PATH — it derives the target triple from `rustc -vV`
  and exits 127 without it (`desktop/scripts/prepare-sidecar.sh:13-22`)
- builds the web UI into `desktop/src-tauri/resources/webui`
- builds `cmd/loom` into `desktop/src-tauri/binaries/loom-<target-triple>`
- builds fleet-db into
  `desktop/src-tauri/binaries/fleet-db-<target-triple>` when fleet-db is
  available; otherwise it warns and the local runtime will need `FLEET_DB_BIN`
  (`desktop/scripts/prepare-sidecar.sh:57-68`)

If fleet-db is not in `../fleet-db`, run:

```sh
FLEET_DB_REPO=/path/to/fleet-db npm run dev
```

### Faster inner loop

After changing Go sidecar code or frontend code, rebuild and relaunch without a
full cycle (`desktop/package.json:8-9`):

```sh
npm --prefix desktop run refresh:app     # rebuild bundle, stop + restart the app and its sidecars
npm --prefix desktop run refresh:webui   # rebuild just the bundled web UI
```

`refresh:app` quits `Loom Agents.app`, kills the `loom` and `fleet-db` sidecars
by bundle path, rebuilds, and reopens (`desktop/scripts/refresh-app.sh`).

## Local App Bundle

Build a local `.app` bundle:

```sh
npm --prefix desktop run build
```

From the repo root, open the built app with:

```sh
open -n "desktop/src-tauri/target/release/bundle/macos/Loom Agents.app"
```

From inside `desktop/`, the same path is:

```sh
open -n "src-tauri/target/release/bundle/macos/Loom Agents.app"
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

`npm run build:dmg` produces an unsigned/ad-hoc DMG suitable for local testing
only. For a DMG that launches on other Macs without Gatekeeper warnings, produce
a Developer ID–signed, notarized, and stapled build — see
[Signed Release Build](#signed-release-build) below. Release-channel metadata and
automatic update publication are still separate release tasks.

## Signed Release Build

A distributable build must be signed with a Developer ID Application certificate,
notarized by Apple, and stapled. There are two supported paths.

### Local (one-shot)

Requirements: the Developer ID Application certificate in your login keychain and
a `notarytool` keychain profile created once with:

```sh
xcrun notarytool store-credentials <profile-name> \
  --apple-id <you@example.com> --team-id BN879H59CY --password <app-specific-password>
# or, with an App Store Connect API key:
xcrun notarytool store-credentials <profile-name> \
  --key AuthKey_XXXX.p8 --key-id <key-id> --issuer <issuer-id>
```

Then build, sign, notarize, staple, and verify a DMG with:

```sh
NOTARY_PROFILE=<profile-name> npm --prefix desktop run release:macos
```

This runs `desktop/scripts/release-macos.sh`, which signs the `.app` and the
bundled `loom`/`fleet-db` sidecars with hardened runtime, notarizes and staples
the app, wraps it in a DMG, then notarizes and staples the DMG. The verified
artifact lands at `desktop/dist-release/Loom-Agents-<version>-aarch64.dmg`, and
the script prints a ready-to-run `gh release create` command. Apple's notary
service typically takes 2–15 minutes, during which the run blocks on `--wait`.

Optional: `brew install create-dmg` for a prettier drag-to-Applications DMG
layout (the `hdiutil` fallback already includes an `/Applications` symlink).

**Installing the DMG — must go in `/Applications`.** Open the DMG and drag
**Loom Agents** onto the **Applications** shortcut. Do **not** double-click the
app inside the mounted DMG or run it from `~/Downloads`: macOS **App
Translocation** runs a freshly-downloaded (quarantined) app from a read-only,
randomized path, and the bundled `loom` sidecar cannot start there. The app
detects this and shows a "Move Loom to Applications" notice instead of hanging
(`desktop/src-tauri/src/lib.rs:36-47` exposes the `needs_relocation` command;
`desktop/src/main.ts:248-264` renders the notice). Once it lives in
`/Applications`, translocation no longer applies and the runtime starts normally
(first launch shows the usual Gatekeeper consent).

### Cutting a release candidate (local)

To produce a versioned RC artifact, set `APP_VERSION` so the DMG, the suggested
tag, and the title all carry the `-rcN` suffix (this only renames the artifact;
the bundle's `CFBundleShortVersionString` stays the `tauri.conf.json` value,
because macOS `CFBundleVersion` must be numeric):

```sh
NOTARY_PROFILE=<profile-name> APP_VERSION=0.1.0-rc1 \
  npm --prefix desktop run release:macos
```

Output: `desktop/dist-release/Loom-Agents-0.1.0-rc1-aarch64.dmg`. Write a checksum
manifest next to it and publish as a GitHub **prerelease** with both files:

```sh
( cd desktop/dist-release && shasum -a 256 Loom-Agents-0.1.0-rc1-aarch64.dmg > SHA256SUMS.txt )

gh release create desktop-v0.1.0-rc1 \
  desktop/dist-release/Loom-Agents-0.1.0-rc1-aarch64.dmg \
  desktop/dist-release/SHA256SUMS.txt \
  --repo tysonthomas9/loomcli --target main --prerelease \
  --title "Loom Agents 0.1.0-rc1 (Apple Silicon)" \
  --notes "Release candidate. Signed + notarized, Apple Silicon (arm64). Install: open the DMG and drag Loom Agents to /Applications."
```

Notes:
- The RC binary is built from the working tree, so the tagged commit does not
  contain the exact source unless you commit the desktop changes first.
- **Reinstalling over an existing `/Applications/Loom Agents.app`**: a scripted
  `ditto`/`cp` overwrite fails with `Operation not permitted` (macOS App
  Management protection on an already-launched bundle). Drag-replace via Finder
  (it prompts for auth), or remove the old bundle first, then copy.

### CI (tag-triggered)

`.github/workflows/desktop-release.yml` builds, signs, notarizes, staples, and
uploads the DMG to a draft GitHub release when a `desktop-v*` tag is pushed:

```sh
git tag desktop-v0.1.0 && git push origin desktop-v0.1.0
```

It requires these repository secrets (Settings → Secrets and variables → Actions):

| Secret | Purpose |
| --- | --- |
| `APPLE_CERTIFICATE` | base64 of a `.p12` export of the Developer ID Application cert |
| `APPLE_CERTIFICATE_PASSWORD` | password used when exporting the `.p12` |
| `APPLE_SIGNING_IDENTITY` | e.g. `Developer ID Application: Tyson Kuthur Thomas (BN879H59CY)` |
| `APPLE_API_ISSUER` | App Store Connect API key issuer id |
| `APPLE_API_KEY_ID` | App Store Connect API key id |
| `APPLE_API_KEY_P8` | contents of the `AuthKey_XXXX.p8` file |
| `FLEET_DB_TOKEN` | optional; token to checkout the private `fleet-db` repo |

Export the cert as base64 with:

```sh
security find-certificate -c "Developer ID Application" -p   # confirm it exists
# In Keychain Access: export the identity (cert + private key) as cert.p12, then:
base64 -i cert.p12 | pbcopy
```

Both paths currently build **Apple Silicon (arm64) only**; a universal (Intel +
ARM) build is a future enhancement.

## Manual Installation

For local manual testing:

1. Build with `npm --prefix desktop run build`.
2. Copy or drag `desktop/src-tauri/target/release/bundle/macos/Loom Agents.app` into
   `/Applications`.
3. Open `/Applications/Loom Agents.app`.
4. Let the app start the local runtime.
5. Open a workspace window from the app once the runtime is healthy.

For DMG testing:

1. Build with `npm --prefix desktop run build:dmg`.
2. Open the DMG under `desktop/src-tauri/target/release/bundle/dmg/`.
3. Drag `Loom Agents.app` into `/Applications`.
4. Launch `/Applications/Loom Agents.app`.

If macOS blocks the unsigned build, use a locally signed/notarized build for
release testing. Avoid documenting a bypass as the supported install path.

## App Data

The app-managed runtime uses an app-native data directory:

```text
~/Library/Application Support/Loom/data
```

The app and LaunchAgent set both of these
(`internal/cli/local/launchagent.go:66-72`):

```text
LOOM_CONFIG_DIR="$HOME/Library/Application Support/Loom/data"
LOOM_DESKTOP_DATA_DIR="$HOME/Library/Application Support/Loom/data"
```

Resolution order when neither is set is `LOOM_DESKTOP_DATA_DIR` →
`LOOM_CONFIG_DIR` → `~/Library/Application Support/Loom/data`
(`internal/cli/local/runtime.go:106-128`). Inside it you will find
`runtime.json`, `state.json`, `logs/{loom-serve,loom-local-service,loom-daemon}.log`,
`fleet-db/redis-snapshot.json`, and `workspaces/<name>/`.

Product data must not be stored inside `Loom Agents.app` because app updates replace
the bundle. A standalone CLI install may still use `~/.loom`; do not silently
merge or move that data into the app directory.

## Local Service

The desktop runtime can be controlled through the bundled CLI. When testing an
installed app, use:

```sh
"/Applications/Loom Agents.app/Contents/MacOS/loom" local status --json
"/Applications/Loom Agents.app/Contents/MacOS/loom" local logs
"/Applications/Loom Agents.app/Contents/MacOS/loom" local drain
"/Applications/Loom Agents.app/Contents/MacOS/loom" local resume
"/Applications/Loom Agents.app/Contents/MacOS/loom" local stop
```

The persistent service is a per-user LaunchAgent:

```text
~/Library/LaunchAgents/com.loom.local.plist
```

Install or replace it:

```sh
"/Applications/Loom Agents.app/Contents/MacOS/loom" local install-service
```

Remove it:

```sh
"/Applications/Loom Agents.app/Contents/MacOS/loom" local uninstall-service
```

`install-service` boots out any existing plist, rewrites it, and bootstraps it
with `launchctl bootstrap gui/$(id -u)`, falling back to `launchctl load`
(`internal/cli/local/launchagent.go:79-113`). It records the *resolved* path of
the currently running `loom` binary (symlinks evaluated,
`internal/cli/local/launchagent.go:141-158`) — so reinstall the service after
moving or replacing the app bundle.

The LaunchAgent runs `loom local service`, which starts `loom serve --bind
--port --fleet-mode` as a detached child (`internal/cli/local/local_cmd.go:241-249`)
and supervises `loom daemon` with backoff (`internal/cli/local/daemon.go:29-45`).
Background agents should survive closing all app windows and restart after user
login.

The full `loom local` command set is `service`, `start`, `status`, `stop`,
`restart`, `install-service`, `uninstall-service`, `drain`, `resume`, `logs`
(`internal/cli/local/local_cmd.go:128`).

## Verification

Run these checks after a local app build:

```sh
npm --prefix desktop run build
open -n "desktop/src-tauri/target/release/bundle/macos/Loom Agents.app"
```

Then verify:

- The app window becomes visible and loads the workspace UI after runtime
  health.
- `loom local status --json` reports `healthy: true`
  (`internal/cli/local/runtime.go:50-52`); the same payload carries `pid`,
  `serve_pid`, `data_dir`, `url`, `port`, `executable`, `binary_hash`, `build`
  and `claims_paused`.
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
"/Applications/Loom Agents.app/Contents/MacOS/loom" local status
"/Applications/Loom Agents.app/Contents/MacOS/loom" local logs
launchctl print gui/$(id -u)/com.loom.local
```

## Updates

**This flow is a target, not a procedure you can run.** There is no in-app
updater and no update-channel metadata; see [Release Gaps](#release-gaps).
Today, updating means building or downloading a new DMG and drag-replacing the
bundle, then reinstalling the LaunchAgent (it records the resolved binary path).

The release update flow must treat the app bundle and sidecars as one unit:

1. Download the app update.
2. Mark the local runtime as draining.
3. Stop new task claims.
4. Let active agents finish, or stop them according to the release policy.
5. Flush fleet-db/miniredis and runtime metadata.
6. Stop the LaunchAgent.
7. Replace `Loom Agents.app`.
8. Restart the LaunchAgent.
9. Resume claims.
10. Restore workspace windows.

Do not hot-swap the bundled `loom` or fleet-db sidecar while agents are
running. If a user also has a standalone CLI installed, the app update must not
rewrite that CLI without explicit user consent.

## Troubleshooting

If the app opens but the workspace never loads, check:

```sh
"/Applications/Loom Agents.app/Contents/MacOS/loom" local status --json
"/Applications/Loom Agents.app/Contents/MacOS/loom" local logs
```

If the app bundle path is missing after build, confirm the command was run from
the expected directory:

```sh
ls "desktop/src-tauri/target/release/bundle/macos/Loom Agents.app"
```

If fleet-db is not bundled, make sure the sibling repo exists or set
`FLEET_DB_REPO`:

```sh
FLEET_DB_REPO=/path/to/fleet-db npm --prefix desktop run build
```

If the LaunchAgent is stale after moving the app, reinstall it:

```sh
"/Applications/Loom Agents.app/Contents/MacOS/loom" local uninstall-service
"/Applications/Loom Agents.app/Contents/MacOS/loom" local install-service
```

If the local runtime appears stuck, drain and restart it:

```sh
"/Applications/Loom Agents.app/Contents/MacOS/loom" local drain
"/Applications/Loom Agents.app/Contents/MacOS/loom" local stop
"/Applications/Loom Agents.app/Contents/MacOS/loom" local start
"/Applications/Loom Agents.app/Contents/MacOS/loom" local resume
```

`loom local logs` prints paths, it does not tail. The three files are
`<dataDir>/logs/loom-serve.log`, `loom-local-service.log` and `loom-daemon.log`
(`internal/cli/local/runtime.go:146,207,211`).

Keep service logs and `runtime.json` with bug reports, but redact secrets,
tokens, API keys, and repo credentials before sharing diagnostics. There is no
automatic redaction — do it by hand.

## Release Gaps

The current repository can build and manually install a development `.app`, and
can produce a Developer ID–signed, notarized, stapled DMG (see
[Signed Release Build](#signed-release-build)). Before shipping to users, finish:

- ~~code signing and notarization~~ — done (local `release:macos` + CI workflow)
- ~~signed DMG packaging~~ — done (notarized + stapled DMG)
- universal (Intel + ARM) build; current builds are arm64 only
- update metadata generation and hosting
- updater UI and rollback behavior
- first-run background service consent
- app data migration/import from standalone CLI data
- CLI install/link flow and PATH conflict warnings
- diagnostics export with secret redaction
- full visual regression coverage for desktop workflows
- tray / menu-bar status item (only the File and Window "New Workspace Window"
  menu items exist today, `desktop/src-tauri/src/lib.rs:158-186`)

Re-verified 2026-07-24: all unstruck items above are still gaps.

## Related

- [`desktop-app-runtime-spec.md`](desktop-app-runtime-spec.md) — the product
  contract this runbook operationalizes: LaunchAgent, data location, CLI
  coexistence, multi-window, runtime modes, and which of them shipped.
- [`local-mode-product-spec.md`](local-mode-product-spec.md) — the local-mode
  topology the app packages.
- [`daemon-agent-runtime-architecture.md`](daemon-agent-runtime-architecture.md)
  — what the supervised `loom daemon` does.
- `desktop/README.md` — the Tauri package README (shorter, same model).
- `.github/workflows/desktop-release.yml` — the tag-triggered release job.
- `docs/loom-glossary.md` — read before writing about "backend", "fleet-db" or
  "local mode".
