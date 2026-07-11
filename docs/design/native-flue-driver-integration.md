# Native Flue Driver Integration

**Status:** implementation note
**Date:** 2026-06-06

## Direction

Loom should not generate Flue projects as the primary driver integration. A
driver is a normal Flue-authored TypeScript project. Flue owns authoring,
dependency resolution, build output, and runtime semantics. Loom and FleetDB own
registration, immutable driver versions, driver runs, task runs, leases,
artifacts, and orchestration state.

Registration is an explicit deployment action. A built Flue Node artifact is
registered into FleetDB as a `DriverVersion`; it is not self-registered by a
runtime process on startup. The active driver version changes only when the
registering operator explicitly activates the version.

For workflow authors, see
[`workflow-driver-authoring-guide.md`](workflow-driver-authoring-guide.md) for
the split between platform-owned `DriverRun` behavior and explicit TypeScript
workflow policy.

## Removed Generated-Project Code

The old `loom driver publish <.loom/workflows/name.ts>` path was the wrong
primary surface and has been removed from the native path. It:

- requires source under `.loom/workflows`;
- writes a hidden `.flue/loom-sources` copy;
- writes a hidden `.flue/loom-runtime/context.ts` adapter;
- writes a hidden `.flue/workflows/<name>.ts` wrapper;
- invokes `flue build` from Loom; and
- records that generated bundle as the driver version.

Those pieces are not a compatibility shim to preserve. Tests should assert that
native registration stages only the built Flue artifact, and that manifests
containing generated-project refs such as `workflow_ref` or `source_bundle_ref`
are rejected.

## Minimal Native Contract

The first supported runtime is a stored Flue Node artifact:

1. The user or agent builds a Flue project with `flue build --target node`.
2. `loom driver register --flue-dist ./dist --name epic-runner` stages the
   already-built artifact under Loom's driver bundle store.
3. Loom writes a registration manifest into that staged bundle.
4. FleetDB records an immutable `DriverVersion` containing:
   - `runtime = flue-node`;
   - `entrypoint = run`;
   - `workflow_name`;
   - `driver_id` and `driver_name`;
   - `server_ref = dist/server.mjs`;
   - `source_ref` and `source_digest` when supplied by the build/deploy step;
   - `bundle_digest`;
   - Flue/Loom SDK/runtime metadata;
   - provenance and capability fields; and
   - the `DriverVersion.bundle_digest` stored outside the manifest to avoid a
     self-referential digest field.
5. If `--activate` is set, Loom updates the `Driver.active_version_id` pointer
   only after the version validates and is stored.
6. The existing Loom executor invokes the registered `server_ref` through the
   Flue local IPC launcher.

Hosted Flue endpoints are a later runtime variant. They need signed invocation,
scoped credentials, cancellation propagation, and event/log attachment. They
should use the same FleetDB `DriverVersion` model but a different runtime
manifest shape.

## Edge Cases

- Duplicate registration of the same bundle digest for a driver is idempotent:
  it returns the existing passed version.
- A new bundle digest creates a new immutable version.
- Invalid artifact layout, missing `server.mjs`, unsafe refs, or malformed
  manifest metadata fails registration and never activates a version.
- Driver-run retry must use FleetDB idempotency and task-run leases; the SDK
  should hide lease token plumbing but preserve tokens across Flue awaits.
- Cancellation must flow from Loom's claimed `DriverRun` context into the Flue
  invocation process.
- Concurrent driver runs for the same epic rely on FleetDB active-run admission
  and task-run claim leases to prevent duplicate child task ownership.
- Flue runner stdout/stderr is attached to the completed FleetDB `DriverRun`
  through output metadata (`logs_ref`, captured tail, and event count). Child
  task logs/artifacts attach to child `TaskRun` records through existing
  `logs_ref`, `artifacts_ref`, and artifact IDs.
