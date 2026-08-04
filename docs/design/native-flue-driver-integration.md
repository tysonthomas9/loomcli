# Native Flue Driver Integration

> **Status:** Current · implementation note, verified against
> `internal/driver/register.go` *2026-07-23*
> **Date:** 2026-06-06

## Direction

Loom should not generate Flue projects as the primary driver integration. A
driver is a normal Flue-authored TypeScript project. Flue owns authoring,
dependency resolution, build output, and runtime semantics. Loom and FleetDB own
registration, immutable driver versions, driver runs, task runs, leases,
artifacts, and orchestration state.

Registration is an explicit action by an operator or an API caller — never a
side effect of a runtime process starting up. A built Flue Node artifact is
registered into FleetDB as a `DriverVersion`. The active driver version changes
only when the registering caller explicitly activates the version.

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

## Second Registration Path (Shipped Later)

`loom driver register --flue-dist` is no longer the only way a `DriverVersion`
is created. Two more paths shipped after this note was written, and both
converge on the same immutable `DriverVersion` + `dist/server.mjs` contract.

**Server-side build from a file set.** `POST /api/workspaces/{ws}/workflows/{name}/versions`
accepts `{files, entrypoint, activate}`
(`internal/webui/handlers/workflows/module.go:39`, request shape at `:40-44`)
and runs `flue build` itself in a temporary project root
(`internal/workflows/workflows.go:313-316`, builder at `:732`). It does write a
build root — a generated `package.json` plus symlinked `@loom/sdk` and
`@flue/runtime` under `node_modules` (`writeWorkflowBuildProject`,
`internal/workflows/workflows.go:620`) — but it stops there. The narrower rule
above still holds: the workflow files are author-written and built as-is, and
Loom never synthesizes wrapper or adapter *source*, no `.flue/loom-sources`
copy, and no `.flue/loom-runtime/context.ts`.

**Lazy builtin registration.** The workflows shipped inside the binary
(`epic-runner`, the task runners, `github-review-agent`; `//go:embed` at
`internal/workflows/workflows.go:35-51`) register themselves on first invocation
through `EnsureBuiltinWorkflow` (`internal/workflows/workflows.go:140`). This is
the one case where registration is triggered by a request rather than an
operator, and it is bounded: a builtin's source is compiled into the binary, so
"self-registering" cannot introduce unreviewed code.

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

## Related

- [`workflow-driver-authoring-guide.md`](workflow-driver-authoring-guide.md) —
  the current platform contract for authors.
- [`taskrun-queue-and-worker-pool.md`](taskrun-queue-and-worker-pool.md) — how
  child `TaskRun`s are enqueued and executed.
- [`driver-op-http-api.md`](driver-op-http-api.md) — the HTTP surface a
  registered bundle talks to at runtime.
- [`fleetdb-agent-platform-v2-proposal.md`](fleetdb-agent-platform-v2-proposal.md)
  — the V2 vision this note narrows.
