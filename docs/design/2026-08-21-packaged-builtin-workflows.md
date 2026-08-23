# Packaged built-in workflows (desktop embedded runtime)

Status: **Slice 1 (DEV-V5-36) and Slice 2 (DEV-V5-37) landed.** Signing/notarization
(Slice 3, DEV-V5-38), the reuse/upgrade decision (Slice 6, DEV-V5-41) and the full
desktop-acceptance tripwire harness (Slice 7, DEV-V5-42) are tracked separately.

This is the durable record DEV-V5-31 deferred to the app slice: what the desktop
app embeds, how the running `loom` finds and trusts it, and how it fails closed.

## Problem

The desktop app must run Loom's built-in Flue workflows (`epic-runner`,
`github-review-agent`) with **no developer checkout, no Flue compiler, and no
`node` on `PATH`** — while never silently downgrading to compiling untrusted
TypeScript at runtime. The solution ships each workflow as a **pre-built,
digest-pinned Flue `dist/`** plus a **pinned Node** inside the `.app`, and makes
the binary refuse to run anything else on a packaged/desktop build.

## Layer map (who owns what)

| Concern | Owner |
|---|---|
| Artifact format, resolver, trust, `readyz` roll-up | `internal/workflows/packaged`, `internal/noderuntime` (Slice 1) |
| Packaging CLI (`loom workflow package-builtin`) | `internal/cli/workflow` (Slice 1) |
| Embedding both built-ins + Node into the `.app`, ad-hoc build | `desktop/scripts/prepare-sidecar.sh`, `tauri.conf.json` (Slice 2) |
| Proof harnesses | `scripts/test-packaged-builtin-devbox.sh` (no app), `scripts/test-packaged-builtin-app.sh` (the DoD) |
| Developer-ID signing, entitlements, notarization | Slice 3 (DEV-V5-38) |
| Reuse/upgrade of an already-registered built-in | Slice 6 (DEV-V5-41) |
| Acceptance tripwires, `results.json`, update candidate | Slice 7 (DEV-V5-42) |

## Bundle layout

A locally-built `Loom Agents.app` (ad-hoc, this slice):

```text
Loom Agents.app/Contents/
├── MacOS/
│   ├── loom            # sidecar CLI + local runtime (ExpectedIndexDigest baked)
│   ├── fleet-db        # control-plane data service
│   ├── node            # pinned Node runtime (internal/workflows/NODE_VERSION)
│   └── loom-desktop    # Tauri shell
└── Resources/
    ├── builtin-workflows/
    │   ├── index.json
    │   ├── epic-runner/dist/{server.mjs, node_modules/@loom/sdk/…}
    │   └── github-review-agent/dist/{server.mjs, node_modules/@loom/sdk/…}
    ├── licenses/node-LICENSE
    └── webui/
```

`node` is a Tauri `externalBin` (`Contents/MacOS/node`); `builtin-workflows/` and
`licenses/` are Tauri `resources` (`Contents/Resources/…`). Both are produced by
`prepare-sidecar.sh` and are `.gitignore`d — never committed.

### `index.json` schema (version `1`)

`internal/workflows/packaged`.`Index` / `Entry`. The `@loom/sdk` is an **external**
in each `server.mjs`; its runtime files ship under `dist/node_modules/@loom/sdk/`
(`package.json`, `index.js`, `internal.js`, `driver.js`, `runner.js`,
`runtime-adapters.js`). The index carries no native addons and no symlinks.

```json
{
  "schema_version": "1",
  "flue_commit": "492bf47b9f3d6c379d00471523987b8fe9511f7d",
  "node_version": "22.20.0",
  "target": "aarch64-apple-darwin",
  "builtins": {
    "epic-runner": {
      "path": "epic-runner/dist",
      "entrypoint": "workflows/epic-runner.ts",
      "source_digest": "sha256:…",
      "artifact_digest": "sha256:…",
      "runners": [
        {"name": "daytona-task-runner", "kind": "flue-workflow", "entrypoint": "daytona-task-runner"},
        {"name": "local-task-runner", "kind": "flue-workflow", "entrypoint": "local-task-runner"}
      ]
    },
    "github-review-agent": {
      "path": "github-review-agent/dist",
      "entrypoint": "workflows/github-review-agent.ts",
      "source_digest": "sha256:…",
      "artifact_digest": "sha256:…",
      "runners": [
        {"name": "github-review-task-runner", "kind": "flue-workflow", "entrypoint": "github-review-task-runner"}
      ]
    }
  }
}
```

`ExpectedIndexDigest` (`sha256:` of the canonical `index.json` bytes) is baked into
the sidecar `loom` at build time via `-ldflags -X …packaged.ExpectedIndexDigest=…`.
`loom workflow readyz --json` exposes it as `builtin_runtime.expected_index_digest`.

## Node resolver order (`internal/noderuntime`)

Every host-side Flue exec site obtains `node` from one place, in this order
(`resolve()`); the executable directory is **symlink-resolved** first
(`EvalSymlinks`, so `/usr/local/bin/loom -> …/Contents/MacOS/loom` still finds the
sibling):

1. `LOOM_NODE_BIN` — developer override → `source=override`. An invalid value is a
   hard error, never a fallback.
2. Bundled (`source=bundled`), first hit wins:
   `<exeDir>/node`, `<exeDir>/node.exe`, `<exeDir>/node-<triple>`,
   `<exeDir>/node-<triple>.exe`, `<exeDir>/../Resources/node-runtime/bin/node`,
   `<exeDir>/../Resources/resources/node-runtime/bin/node`. Only the
   `Contents/MacOS/node` sibling ships today; the `Resources/node-runtime` probes
   are a reserved second location.
3. `PATH` `node` → `source=path`.

`readyz` reports the resolved `builtin_runtime.node.{source,path}`; on the desktop
app that is always `bundled` at `Contents/MacOS/node`.

## Fail-closed policy (dual-keyed)

`packaged.FailClosed() == IsPackagedBuild() || IsDesktop()`:

- `IsPackagedBuild()` — an `ExpectedIndexDigest` was baked in.
- `IsDesktop()` — the process runs under `LOOM_LOCAL_RUNTIME=desktop`.

When either is true, a missing/mismatched artifact is **fatal** — the runtime
never compiles. The two error shapes:

| Condition | Type | HTTP | Message carries |
|---|---|---|---|
| No artifact for a required built-in | `ErrNotPackaged` (plain sentinel) | 500 | `builtin_artifact_missing … desktop packaging error; reinstall Loom` |
| Artifact present but digest mismatch | `VerificationError` (wraps `domain.ErrInvalid`) | 4xx | `builtin_artifact_invalid: <name>: <field> mismatch … reinstall Loom` |

`ErrNotPackaged` must **not** wrap `domain.ErrNotFound`: a fail-closed error may
never collapse to a 404 "workflow not found" that hides the cause. Off the
fail-closed path (a plain dev box, neither packaged nor desktop),
`builtin_runtime_ready == authoring_ready` and the compile fallback still applies.

## Trust & provenance

Registrations from this lane are stamped `manifest.provenance == "packaged_builtin"`
and `manifest.trust_level == "trusted"`, with `manifest.packaged_index_digest`
equal to the app's baked digest. The digest chain (source → artifact → index →
baked `ExpectedIndexDigest`) is what earns the trust; there is no signature check
at this layer (that is the OS's job — see Signing).

## Native-addon audit

`loom workflow package-builtin` statically audits each `dist/` and refuses to
package one that contains a native addon (`*.node`), a `dlopen`, a non-allowlisted
bare specifier, or a symlink. Both current built-ins audit clean
(`native_files=[]`, `bare_specifiers=[]`, `dynamic_bare_specifiers=[]`,
`dlopen=false`, `symlinks=[]`). The only allowlisted bare specifiers are
`@loom/sdk` and `@loom/sdk/*` (the external SDK). **Rule:** if the audit or the
load smoke ever finds a native addon, revisit `disable-library-validation` /
entitlements in the *same* change — never weaken the audit silently.

## Signing

- **This slice (2):** ad-hoc signature only (whatever `tauri build` emits). Runs on
  the build machine; blocked by Gatekeeper elsewhere. Sufficient to prove the
  runtime because Mach-O signatures are content-based and the test never uses
  LaunchServices/`open`.
- **Slice 3 (DEV-V5-38):** Developer-ID signing of every nested binary
  **including `node`**, hardened-runtime entitlements, and notarization. Until it
  lands, `desktop-release.yml` builds and signs but fails at the notary (the
  nodejs.org `node` is not yet hardened-runtime signed/entitled by us) — the
  re-sign loop in `release-macos.sh` is marked `# node re-sign + entitlements:
  DEV-V5-38`.

## Build-host environment

`prepare-sidecar.sh` (and CI's `builtin-bundle-pin`) need:

- `FLUE_REPO` — a Flue checkout at `internal/workflows/FLUE_COMMIT` with
  `pnpm install` + `pnpm --filter @flue/runtime --filter @flue/cli build`. The pin
  gate in `rebuild-builtin-bundle.sh` is authoritative; `ALLOW_FLUE_PIN_DRIFT=1`
  bypasses it **and** breaks the load smoke (a drifted Flue drops the one-shot
  `{type:'ready'}` IPC handshake, so `smoke-load-server.mjs` times out).
- Network (or `LOOM_NODE_TARBALL` + `LOOM_NODE_SHASUMS`) for the pinned Node
  tarball, SHA-verified against `SHASUMS256.txt`.
- Host Node ≥ 22.18 for the Flue build; `go`, `rustc`/`cargo`, `npm`, `curl`,
  `shasum`, `tar`.

Escape hatches: `LOOM_SKIP_BUILTIN_ARTIFACTS=1` (embed Node, no artifacts → the
app is *not* a packaged build and fails closed on desktop); `LOOM_BUILTIN_PREBUILT_DIST_DIR`
(reuse a prebuilt `dist/` per name instead of rebuilding).

## Measured sizes

_To be recorded from a real `aarch64-apple-darwin` build_ (`du -sh` printed by
`prepare-sidecar.sh`): `Contents/MacOS/node`, each `builtin-workflows/<name>/`,
the `.app` total, and the `.dmg` delta. Expected order of magnitude: `node`
≈ 110 MB; each artifact a few MB. This environment cannot build the `.app`, so the
numbers are left for the first real build to fill in.

## Known gaps (deliberately out of this slice)

- **Symlink handling in `internal/cli/local/runtime.go`** (`localEnv`,
  `desktopRuntimePath`, `bundledExecutable`) still uses `os.Executable()` without
  `EvalSymlinks`, and `desktopRuntimePath` prepends `/opt/homebrew/bin:/usr/local/bin`
  to `PATH` — the reason DEV-V5-34 mandates tripwires. **Slice 7 / DEV-V5-34.**
- **Authoring readiness** (`commandAvailable("node")` for the Flue CLI) is
  intentionally separate from the runtime resolver. **Slice 4 (DEV-V5-39).**
- **Notarization + `node` entitlements.** **Slice 3 (DEV-V5-38).**
- **Reuse/upgrade** of an already-registered built-in at an older digest.
  **Slice 6 (DEV-V5-41).** The app test always uses fresh data dirs.
- **`scripts/test-runner-pr-e2e.sh:33`** still references the removed
  `internal/workflows/builtin-dist/epic-runner/dist` `go:embed` tree — unrelated
  to packaging; left as-is.
