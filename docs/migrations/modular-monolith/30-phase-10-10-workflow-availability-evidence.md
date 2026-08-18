# Phase 10.10 Workflow Distribution and Durable Availability Evidence

- **Status:** Green. Paired FleetDB contract and owner implementation, Loom
  distribution lifecycle, focused and race proof, both full repository gates,
  and an isolated packaged-Desktop create, execute, restart, and re-execute
  journey all passed.
- **Stack:** 10.10 Workflow Distribution and durable availability
- **Loom branch:** `modular-monolith-phase10-10-workflow-availability`
- **Loom base:** stack 10.9 at `f87ffd555`
- **Loom implementation head:** `a721ef5b6`
- **FleetDB branch:** `modular-monolith-phase10-10-workflow-availability`
- **FleetDB head:** `188c2a162`
- **FleetDB review:** [BrowserOperator/fleet-db#189](https://github.com/BrowserOperator/fleet-db/pull/189)

## Implemented boundary

Workflow Catalog now distinguishes source validation from durable bundle
availability. Authoring first records a validation-passed version with
`availability_status=pending`; that record is not approvable, activatable,
resolvable, dispatchable, or executable. Workflow Distribution then promotes
the exact staged bundle, verifies its source and bundle digests, and uses the
purpose-scoped system command `RecordVersionAvailability` to record one of:

- `available`, which is the only executable state;
- `retryable_failure`, which retains the deterministic pending bundle for
  restart reconciliation; or
- `permanent_failure`, which fails the version closed and discards its staged
  bytes.

FleetDB owns the atomic availability transition, exact-request replay,
expected-revision conflict, digest equality, and a maximum of three
availability attempts in Redis and Postgres. Loom negotiates mandatory
capability `workflow_catalog.version_availability.v1`; an older FleetDB cannot
silently enter an authoring-only compatibility path.

Managed builtins use exact system-only approve and activate actions after the
availability commit. An already active predecessor remains active through
authoring, promotion, verification, and approval; only the final activation
changes `active_version_id`. Operator authoring keeps approval and activation
explicit.

## Distribution and recovery

Staging writes deterministic pending content beneath
`.loom/drivers/.pending/<version-id>`. Promotion is idempotent when the final
bundle already has the expected content and fails closed on digest drift. A
startup reconciliation pass scans every workspace and every pending version,
recovers its exact staged/final bundle, promotes and verifies it, then records
availability. Retryable recovery failures remain pending and retain their
staging bytes; permanent failures become durably failed. Either outcome is
recorded without preventing unrelated workspace startup.

The former nested
`internal/infra/workflowdistribution/authoring` package is deleted. Its private
filesystem and packaged-builtin mechanisms now live in the single
Workflow Distribution adapter package. The package-shape ratchet therefore
shrinks from 152 to 151 production packages and from 134 to 133 packages
outside `internal/modules`; the 18 capability-module packages are unchanged.

## Fail-closed consumers

The same explicit availability predicate protects every production consumer:

- Workflow Catalog effective/requested resolution and operator/system approve
  and activate commands;
- Driver run admission and execution;
- task-run bundle environment projection;
- native workflow execution;
- WebUI workflow-run submission; and
- the driver management API's active-workflow projection.

There is no empty-value default, read-time normalization, or migration backfill
that converts an old version into available. Test fixtures that model an
executable version set the explicit state just as production data must.

## Required proof matrix

| Required behavior | Authoritative proof | State |
|---|---|---|
| Pending before distribution | Coordinator order test proves author-pending precedes promote, verify, available, and cleanup. | Green |
| Retryable transition fault | Promotion fault records `retryable_failure`, retains staging, and returns the original fault. FleetDB bounds repeated attempts. | Green |
| Permanent and digest-drift fault | Distribution classifies digest drift as permanent; recovery records `permanent_failure` and discards only terminal staging. | Green |
| Restart reconciliation | A new coordinator scans durable pending versions and promotes, verifies, and marks the exact version available. | Green |
| Approval and activation denial | Owner tests reject both actions before any lifecycle-store call when availability is pending. | Green |
| Resolution and execution denial | Catalog resolution, Driver execution, task bridge, native runner, management projection, and WebUI submission reject a non-available bundle. | Green |
| Active predecessor preservation | Managed lifecycle order test observes the old active version at both approval and activation boundaries, changing it only in the final activation. | Green |
| Exact authority and replay | Workflow Catalog validates the action-scoped system authority and exact returned identity/digests; FleetDB Redis/Postgres suites cover replay and conflicts. | Green |
| Capability fail-close | Serve requires `workflow_catalog.version_availability.v1`, and the complete adapter constructor requires both authoring and availability transports. | Green |
| No implicit availability | `VersionAvailable` returns true only for the explicit `available` state; pending, failed, empty, and nil are false. | Green |
| Retired package | Architecture tests reject the deleted nested authoring directory and former authoring compatibility files. | Green |
| Packaged product journey | The real packaged Desktop created a workspace, admitted a repository, authored an enabled planner, completed a real Codex run, survived a service restart with its transcript and design intact, then completed a second real Codex run through the corrected outcome decoder. | Green |

## Verification

FleetDB commit `188c2a162` passed its complete `make gate`, including build,
vet, lint, race-enabled unit and integration suites, coverage, Redis and
Postgres availability contracts, API integration, container E2E,
restart/recovery, and harness evaluation. Its canonical OpenAPI is byte-equal
to Loom's vendored contract at SHA-256
`e4f97729d0aa33be62107b05bb6b3d456dcadb7c8bf412a5c8d34a80b48d70e0`.

The focused Loom owner, application, adapter, composition, Driver, and delivery
suites passed:

```text
FLEET_DB_REPO=../fleet-db-modular-monolith-phase7 \
GOCACHE=/private/tmp/loom-phase10-10-cache \
go test ./internal/modules/workflowcatalog/... \
  ./internal/app/workflowauthoring \
  ./internal/infra/workflowdistribution \
  ./internal/infra/fleetdb \
  ./internal/app/serve \
  ./internal/cli/serve/serveadapter \
  ./internal/driver \
  ./internal/webui/handlers/workflows -count=1
```

The owner and adapter scope also passed with the race detector. After the
packaged proof exposed a canonical outcome-decoding fault, the exact affected
adapter and reconciler packages passed again:

```text
FLEET_DB_REPO=/Users/tyson/codebase/code-agents/rc-2/fleet-db-modular-monolith-phase7 \
GOCACHE=/private/tmp/loom-phase10-10-race-cache \
go test -race ./internal/infra/fleetdb ./internal/driver -count=1
```

The exact affected full packages, all internal package compilation, and the
packaged-builtin embedding test passed. The authoritative paired Loom gate
then passed:

```text
FLEET_DB_REPO=/Users/tyson/codebase/code-agents/rc-2/fleet-db-modular-monolith-phase7 \
FLEET_DB_BIN=/private/tmp/fleetdb-phase10-10-bin \
GOCACHE=/private/tmp/loom-phase10-10-final-gate-cache-3 \
make gate

=== Go quality gates PASSED ===
=== Frontend quality gates PASSED ===
=== All quality gates PASSED ===
```

The final architecture run reported 151 production packages, 133 outside
`internal/modules`, 18 module packages, zero composite Store files, zero
outside-composition Store files, zero legacy handler imports, 72 direct-write
rows, ten capability roots, 108 mutation commands, 71 runtime components, 74
goroutine definitions, six performance records, zero pending decisions, and
all 11 build profiles. Its measured process-tree RSS peak was 924.4 MiB against
the 2 GiB ceiling.

## Packaged product proof record

The implementation built and drove an isolated packaged app at
`/private/tmp/phase10-10-tauri-target/release/bundle/macos/Loom Agents.app`
using FleetDB `188c2a162` and a fresh data directory
`/private/tmp/phase10-10-desktop-data-20260817-b`. Artifact hashes are:

- Tauri executable SHA-256
  `bb8a52ec11014379408e758bc90743b761f7391999890b42e4dadd8d765d4733`;
- Loom sidecar SHA-256
  `460d48204c9cc7c75fe9339daa2e25c1e70cbe09afe8a1d4343f73cd5541d7d9`;
  and
- FleetDB sidecar SHA-256
  `02d3cb32580579b892b9a32fa7ec2588d780990f4cb0b33c3d6e624e2f06896c`.

The source build, all six packaged Flue bundles, frontend, Tauri release app,
and embedded-builtin test passed. Computer Use then drove the actual packaged
window through these product operations:

1. Created workspace `PHASE10-10-PROOF-20260817`.
2. Admitted the clean fixture repository `phase10-10-proof-repo` at source
   commit `6a2303a0608929341f55397a5f6960fe384ef9e1`.
3. Created and enabled Behavior Planner
   `agt-phase10-10-availability-planner-aa726c2c` on
   `internal.task.ready` with the real Codex backend.
4. Created task `PHASE10-10-PROOF-20260817-1` through the UI. Run
   `automation-run-4c58a5bc09d71355ce20e499c6913e51` completed with exit 0,
   a finalized transcript, and a persisted design; the task converged to
   Review with clickable agent and task attribution.
5. Stopped only the isolated packaged runtime, rebuilt the package with the
   canonical outcome decoder, and relaunched it against the same data
   directory. The workspace, repository, agent, completed run, transcript,
   task, and design all reappeared. The packaged CLI reported the restarted
   runtime healthy at `http://127.0.0.1:53387` with the corrected Loom sidecar
   hash above.
6. Created task `PHASE10-10-PROOF-20260817-2` through the UI after restart.
   Run `automation-run-27f3c18f574bac50cc380ea506aed83b` completed with exit
   0 in 1m51s, a finalized transcript, and a second persisted design. The task
   converged to Review and the clean fixture repository remained unchanged.
7. After the gate-required wire-DTO file relocation, rebuilt the final package
   and relaunched it a second time against the same data. Both Review cards,
   the enabled agent, and the admitted repository restored in the actual UI.
   The packaged CLI reported healthy at `http://127.0.0.1:52840` with the final
   Loom sidecar hash above.

Screenshots and hashes:

- `/private/tmp/phase10-10-available-workflow-completed.png` —
  `952b4dff0e273f495035a833b8a8fca3955d9239ec689ff07215bc59cdbead3a`;
- `/private/tmp/phase10-10-task-review-design.png` —
  `fd6d740f89b72c98081608cfcfa041d71cdad30e96b4166dc7018a02ef558972`;
  and
- `/private/tmp/phase10-10-post-restart-second-run.jpeg` —
  `a709106d82ae768bd0607c839bbdf2fdf961ec69866cb46d417b0720e886a0fe`.

### Product-found outcome decoder defect

The first packaged completion exposed a warning in the durable outcome
reconciler. FleetDB correctly emitted canonical snake-case JSON such as
`workspace_key`, `run_id`, and `occurred_at`, but Loom decoded that response
directly into the Execution-owned `DriverRunOutcome`, whose transport-neutral
fields have no FleetDB JSON tags. Go therefore left identity and time fields at
zero values while decoding simple names such as `status` and `attempt`. Older
tests encoded Loom's owner type and reproduced its CamelCase shape, masking the
producer/consumer drift.

The adapter now owns a private `driverRunOutcomeWire` DTO with the canonical
FleetDB field names and maps it explicitly into the Execution record. Both the
ordinary outcome claim and terminal-recovery claim use the same decoder. The
two literal-contract regressions
`TestDriverRunOutcomeTransportDecodesCanonicalClaimResponse` and
`TestTerminalDriverRunWorkRecoveryQueueTransportDecodesCanonicalClaimResponse`
failed before the fix and pass afterward. Existing claim fixtures now encode
the producer wire shape rather than the owner model.

The restarted server begins at log line 796, timestamp
`2026-08-17T17:43:26.875-07:00`. From that point through the second completed
run, a scan for `reconcile durable run.finished outcome failed`,
`invalid snapshot`, `invalid persisted run outcome for ""`, and
`runtime component pass failed` returned zero matches. The pre-fix segment is
retained in the isolated log as the regression's failure evidence.

The final artifact restart begins at log line 1939, timestamp
`2026-08-17T18:27:54.838-07:00`, and likewise contains no matching outcome or
runtime-component warning.

No foreign Loom process, persistent dogfood stack, fixed port, browser profile,
or Desktop data directory was stopped or reused.

## Next stack

Stack 10.11 makes the composed runtime private and reduces WebUI to delivery.
It must prove construction, start, rollback, and close behavior, route/runtime
parity, exact WebUI topology, and deletion of the parallel `serveadapter` and
screen-oriented coordinator composition paths.
