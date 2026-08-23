# Workflow versioning, update & rollback (built-in track)

Status: **Slice 6 (DEV-V5-41) core + CLI + HTTP landed.** The authoring/versions
UI is Slice 5 (DEV-V5-40); notarization is Slice 3 (DEV-V5-38); the desktop
acceptance tripwire is Slice 7 (DEV-V5-42).

This is the durable record for the locked decisions D1–D7 of the DEV-V5-33
design map: what an app update does to an already-registered built-in workflow,
how a version is identified, and how update / rollback / downgrade behave —
proven without the compiler by `scripts/test-builtin-update-rollback.sh`.

## Problem

Slice 1/2 made the desktop app run a **packaged** built-in workflow from a
digest-pinned Flue `dist/` with no compiler. But a built-in workflow is
long-lived: the app is updated, the packaged artifact changes, and an operator
may want to pin a version or undo a bad update. The open questions were:

- When the app updates and ships a **new** packaged `epic-runner`, does the
  running version change automatically, or is the operator's choice preserved?
- What *is* a version's identity, so an app update that only reshuffles the
  index or a runner set does not mint a spurious new version?
- Can an operator **roll back** to the previously-active version — and can the
  app **downgrade** to an older built-in — when the newer bundle is bad?
- How is all of this proven when authoring a custom version (the compiler) is
  explicitly off the table for the desktop lane?

## Decisions (D1–D7)

| # | Decision |
|---|---|
| **D1** | An app update auto-activates the newly packaged built-in **only on the `auto` track** (the default for a fresh built-in). A **`pinned`** built-in — one an operator explicitly activated/rolled back, or one whose active version is a custom authored build — is **preserved**, and the newer packaged version is surfaced as `update_available` instead. |
| **D2** | A version's identity is its **bundle digest** (`artifact_digest`). Built-in sync **dedups** packaged registrations by `manifest.artifact_digest`: an app update that only churns the `index.json` digest or a runner set never mints a new `DriverVersion`. |
| **D3** | A version is **immutable** (the `DriverVersion` store has no update/delete) and its **staged bundle is retained indefinitely** in the workspace runtime dir — it survives resource-tree replacement, which is what makes rollback and downgrade to a no-longer-shipped built-in possible. |
| **D4** | **Rollback** = activate the recorded previous-active version (or an explicit `--version`), writing a fresh activation record. Rollback **pins** the track. |
| **D5** | `POST …/versions` defaults `activate` to **false** — registering a version no longer silently switches the active one. |
| **D6** | **Downgrade** is symmetric to update: on the `auto` track, an app build whose packaged version is *older* than the active one re-activates the older packaged version. |
| **D7** | The whole lifecycle is **proven via the CLI lane** — `scripts/test-builtin-update-rollback.sh` — with **no dependency on the versions UI (Slice 5) and the compiler never invoked**. |

## The built-in track state machine

A driver carries a `builtin_track` in its activation record. `ResolveBuiltinTrack`
reads it, falling back to a legacy heuristic when the key is absent (a
system-activated `builtin://` version reads as `auto`; anything else with an
active version reads as `pinned`; no active version reads as `auto`).

`BuiltinSyncDecision(track, activeID, packagedID)` is the pure core:

| State | `auto` track | `pinned` track |
|---|---|---|
| no active version | **activate** packaged | **activate** packaged |
| active == packaged | no-op | no-op |
| active != packaged | **activate** packaged (update *or* downgrade, D1/D6) | **update_available** — active preserved (D1) |

Transitions:

- **Fresh built-in** → registered on `auto` (default). App updates follow.
- **Operator `activate --version` / `rollback`** → `pinned`. App updates are held
  and surfaced as `update_available`.
- **Operator `activate --builtin`** (or `sync … --force-track auto` internally) →
  back to `auto`, re-adopting this build's packaged version and following updates.
- **Active custom (authored) version** → reads as `pinned`; an app update
  registers the packaged version inactive and surfaces `update_available`.

## The activation record

Every activation rewrites five driver-metadata keys atomically
(`internal/driver/activation.go`), preserving any `approved_version:*` keys:

| Key | Values | Meaning |
|---|---|---|
| `activation_actor` | `user` \| `system` | Who activated: an operator/dev, or the runtime. |
| `activation_reason` | `registration` \| `builtin_sync` \| `operator` \| `rollback` | Why. |
| `activation_at` | RFC3339 UTC | When. |
| `activation_previous_version_id` | version id (may be empty) | What was active before — the default rollback target. |
| `builtin_track` | `auto` \| `pinned` | Omitted entirely when the track is empty (non-built-in drivers). |

`selected_by` in `workflow versions` surfaces `activation_actor` on the active
row, so `user` vs `system` is visible without reading raw metadata.

## Sync engine

`SyncBuiltinWorkflow` (`internal/workflows/builtin_sync.go`) is the one entry
point the CLI, HTTP, and startup path share:

1. **Look up** this build's packaged artifact (cache keyed by
   name+digest+artifacts-dir+baked-index-digest, robust against cross-test
   poisoning). `ErrNotPackaged` is returned unchanged for the caller to map.
2. **Resolve/register** the packaged version. If a version with the same
   provenance + `artifact_digest` already exists it is reused (D2); otherwise it
   is registered **inactive** (D5) with `provenance=packaged_builtin`,
   `trust=trusted`. A **tampered or missing** *packaged* bundle is repaired by
   re-staging from the packaged tree (never for a custom version).
3. **Apply track policy** via `BuiltinSyncDecision`, then activate (or not),
   writing the activation record with the right actor/reason/track.
4. **Report** `active_bundle_available` (does the active version's staged bundle
   verify?) so a caller can fail closed when the active bundle is gone.

`EnsureBuiltinWorkflow` (startup) drives the same sync on a successful packaged
lookup; on `ErrNotPackaged` it **reuses** an already-staged bundle before failing
closed (packaged/desktop) or compiling (dev binary) — the reuse-before-fail-closed
order is what keeps D3 idempotent when the resource tree is gone.

## API / CLI surface

CLI (`internal/cli/workflow`):

```
loom workflow sync <name>                 # register + apply track policy (auto default)
loom workflow activate <name> --version V [--track auto|pinned]
loom workflow activate <name> --builtin   # adopt packaged version, follow updates (auto)
loom workflow rollback <name> [--version V]# default target: recorded previous active
loom workflow versions <name> [--json]    # newest-first; provenance, selected_by,
                                          # bundle_verified per version; built-in block
                                          # with update_available + packaged version id
```

`--track auto` on a plain `--version` activation is accepted **only** when that
version is this build's packaged version (else `builtin_track_invalid`).
`--builtin` and `--version` are mutually exclusive; one is required.

HTTP (`internal/webui/handlers/workflows`, created by this slice because
DEV-V5-32's surface never landed):

```
GET  /api/workspaces/{ws}/workflows/{name}/versions
POST /api/workspaces/{ws}/workflows/{name}/versions               # activate=false default (D5)
POST /api/workspaces/{ws}/workflows/{name}/versions/{id}/activate # body: {track?}
POST /api/workspaces/{ws}/workflows/{name}/builtin/sync           # body: {force_track?}
POST /api/workspaces/{ws}/workflows/{name}/rollback               # body: {version?}
```

Error mapping: `rollback_target_missing` → 409, `builtin_track_invalid` /
`not_builtin_workflow` → 400, `builtin_active_version_unavailable` → 409,
`ErrNotPackaged` on a fail-closed build → 503, `ErrNotFound` → 404,
`ErrInvalid` → 400.

## Retention & disk cost (D3)

Every registered version keeps its staged bundle under
`LOOM_WORKSPACE_RUNTIME_DIR/.loom/drivers/<name>/<version-id>/dist/` forever;
nothing prunes it. This is deliberate — a retained older bundle is exactly what
lets `rollback` and an `auto`-track `downgrade` re-activate a version whose
packaged tree this build no longer ships.

The demo measured **~32 KiB per staged version** (64 KiB for the two retained
versions `vA`, `vB`) using trivial stub `dist/`s. A real built-in bundle is
dominated by the Flue `dist/server.mjs` + the nested `@loom/sdk` runtime, so
the production per-version cost is on the order of the packaged `dist/` size
(hundreds of KiB to low-MiB), multiplied by the number of versions a machine
has ever activated. **A production pruning policy** (keep the active version +
the last *N* previous, or an age cap) is deferred — see the production-ticket
breakdown below.

## Proof — `scripts/test-builtin-update-rollback.sh`

The demo simulates two app builds with two `loom` binaries that bake different
`ExpectedIndexDigest` values over two single-entry `builtin-workflows` trees
whose `epic-runner` `dist/` differs (stub `server.mjs` A vs B → distinct
`artifact_digest`s → distinct immutable versions). `LOOM_REAL_FLUE_CMD` is
`/usr/bin/false` throughout; **no run is ever created and the compiler is never
invoked.** Against a real embedded fleet-db store and real staged bundles it
proves, in order (observed in `desktop/dist-skeleton/update-rollback-observed-output.txt`):

| Scenario | What it shows | Key observation |
|---|---|---|
| **A. install** | first `sync` registers `vA` and activates it | `activated=true track=auto registered_new=true` |
| **B. update** | app build B on `auto` activates `vB` (D1/D2) | `active=vB previous=vA registered_new=true` |
| **C. downgrade** | app build A on `auto` re-activates `vA` (D6) | `active=vA previous=vB registered_new=false`; `vB` version + bundle survive (D3) |
| **D. rollback** | restore previously-active `vB`, pin (D4) | `Rolled back … to vB (previous vA)`; `track=pinned selected_by=user` |
| **E. pinned keep** | app build A must **not** auto-activate (D1) | `activated=false active=vB(preserved) update_available=true packaged=vA` |
| **F. tamper** | corrupt a staged bundle, re-sync self-heals (D3) | `bundle_verified=false` → `repaired=true` → `bundle_verified=true` |

The **pinned-custom** case (active version is a Flue-authored *custom* build) is
proven at the Go/HTTP layer — `CustomPinnedPreserved`, `CustomBundleDeletedUnavailable`,
`RejectsCustom`, `PinnedUpdateAvailable` — because authoring a custom version
requires the compiler, which D7 forbids in the shell demo. The shell demo
instead exercises the *identical* pinned-preservation mechanism with a
packaged-but-pinned active version (scenario E).

## Production-ticket breakdown

Follow-ups this slice deliberately leaves open:

1. **Versions UI** (Slice 5, DEV-V5-40): the update banner + versions table over
   the HTTP surface created here.
2. **Bundle retention / pruning policy**: keep active + last *N*, or an age cap,
   with a `loom workflow prune` and a startup sweep. Needs the measured
   production per-version cost from a real packaged build.
3. **Cross-machine / fleet rollout**: today the track lives in one store's driver
   metadata; a fleet-wide "pin this version across the workspace" is out of scope.
4. **Signed provenance on rollback targets**: rollback trusts the retained staged
   bundle's digest; pairing it with the notarization work (Slice 3) would let a
   rollback re-verify the bundle's signature, not just its digest.
