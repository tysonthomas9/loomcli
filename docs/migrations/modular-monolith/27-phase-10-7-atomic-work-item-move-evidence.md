# Phase 10.7 Atomic Work Item Move Evidence

- **Status:** Paired FleetDB transaction, Loom owner/application/delivery
  boundary, full repository gates, and packaged-WebUI restart proof green
- **Stack:** 10.7 Atomic Work Item move with paired FleetDB command
- **Loom branch:** `modular-monolith-phase10-07-atomic-work-item-move`
- **Loom base:** stack 10.6 at `6fc600103`
- **FleetDB branch:** `modular-monolith-phase10-07-atomic-work-item-move`
- **FleetDB head:** `7c486449d`
- **FleetDB review:** [BrowserOperator/fleet-db#179](https://github.com/BrowserOperator/fleet-db/pull/179)

## Implemented boundary

Cross-workspace movement is one named application workflow rather than a
sequence of generic Work Items writes. `internal/app/workitemmove` resolves
both Workspace references, rejects a same-workspace move, requires the exact
source revision and caller-generated request ID, and invokes one
`AtomicMover.MoveAtomic` port. It never owns or rewrites Workspace or Work Item
records. A divergent or incomplete owner result fails closed as invalid
persisted state.

The FleetDB adapter lives in the existing `internal/app/serve` composition
package. It maps the application command to the paired low-level FleetDB route
and translates stable revision, idempotency, ineligibility, authorization,
timeout, and availability errors into the Work Items vocabulary. It does not
introduce another repository package or a generic Store facade.

Delivery exposes
`POST /api/workspaces/{ws}/issues/{id}/move`. The canonical request contains
only `target_workspace`, `expected_source_revision`, and `request_id`; unknown
fields and incomplete intents are rejected. The response contains the source
ID, target ID, and replay flag. The browser generates a UUID request ID and
uses the loaded Work Item `updated_at` revision rather than inventing a
client-side version.

The source and target carry symmetric typed lineage. The retired source is
closed and immutable at the owner, HTTP/RPC, and both browser detail surfaces.
It displays a clickable **Moved to** link and exposes no status, assignment,
repository, dependency, comment, delete, review, terminal, or epic-runner
mutation. The target remains editable and displays a clickable **Moved from**
link. The side panel and full-page view share one lineage component so the
same invariant cannot drift between routes.

## Paired FleetDB transaction

FleetDB capability `work_items.atomic_move.v1` owns the durable transition.
The Redis implementation uses one atomic script and the Postgres
implementation uses one SQL transaction. Both implementations:

- authorize the source and target workspaces;
- compare the exact source revision;
- reject assigned, active, claimed, connected, or dependency-bound sources;
- reserve the target ID and the request receipt in the same commit;
- copy the full eligible Work Item state to the target;
- close and stamp the source with immutable target lineage;
- stamp the target with source lineage;
- return the original target for an exact replay;
- reject a request-ID reuse with a different intent; and
- leave both aggregates unchanged on stale revision, collision, wrong type,
  authorization failure, or transaction failure.

Every later aggregate mutation of a moved source is rejected. This is an owner
invariant, not a WebUI-only disabled state.

## Required proof matrix

| Required behavior | Authoritative proof | State |
|---|---|---|
| One owner transaction | Redis script and Postgres transaction suites prove source retirement, target creation, indexes, lineage, events, and receipt commit together. | Green |
| Exact replay and divergent reuse | Redis/Postgres tests return the original target for an exact request and reject a reused request ID with different intent. | Green |
| Stale revision and rollback | Redis/Postgres tests prove stale revision, target collision, wrong type, and injected transaction failure change neither aggregate. | Green |
| Concurrency | Redis/Postgres concurrent exact-command tests converge on one target and one receipt. | Green |
| Authorization | API tests require source and target workspace authority before storage; Loom preserves the typed forbidden result as HTTP 403. | Green |
| Eligibility | Both backends reject assigned, running, claimed, connected, and dependency-bound sources; the browser explains assigned/dependency denial before submission. | Green |
| Immutable source | Redis/Postgres mutation matrices, service/RPC tests, Loom conversion tests, and both browser detail surfaces reject or suppress every later source mutation. | Green |
| Stable Loom seam | Coordinator, composition adapter, transport, handler, canonical OpenAPI, generated Go/TypeScript, capability negotiation, and contract-guard tests pass. | Green |
| Product success and history | The sealed bundle WebUI created two workspaces and a source Work Item, submitted the move, followed both lineage links, and showed the source as read-only and the target as editable. | Green |
| Restart durability | The run-owned packaged runtime was stopped, rebuilt, and restarted with the same data directory; both records and both lineage directions persisted. | Green |

The live browser receives Work Item mutation events and refreshed a second
session before its attempted stale submission. That journey therefore proved
live convergence and a second successful move, not a stale 409. Stale conflict
remains deterministically proven by the Redis, Postgres, Loom transport, and
full WebUI integration suites; this record does not mislabel a synchronized UI
submission as conflict evidence.

## Architecture result

The change preserves the stack 10.6 package topology:

- production packages: `153` total;
- packages under `internal/modules`: `18`;
- production packages outside module roots: `135`;
- one-file packages: `37`;
- one-or-two-file packages: `55`;
- capability module roots: `10`;
- build profiles: `11/11`; and
- pending architecture decisions: `0`.

The direct-write inventory classifies the one application mutation as a named
atomic owner command. Composition imports increase only in the existing
`internal/app/serve` root; CLI and WebUI fanout ceilings do not move. The
architecture gate remains below its 2 GiB process-tree limit, with observed
runs around 1.0–1.2 GiB.

## Verification

FleetDB commits `2338dfd` and `7c48644` were pushed before Loom consumption.
The full FleetDB `make gate` passed, including build, vet, lint, race,
coverage, Redis and Postgres contracts, integration, E2E restart/recovery, and
harness evaluation.

Loom focused suites pass for the coordinator, composition adapter, FleetDB
transport, capability negotiation, Work Items conversion, HTTP handler, and
the cross-workspace integration route. The full frontend Vitest transaction
passes `396` files and `8,754` tests with `1` skipped. The focused detail-view
and detail-panel transaction passes `105/105`; the authoritative frontend
typecheck and production build pass. The complete `make gate` passes after the
generated Go and TypeScript contracts, vendored FleetDB OpenAPI guard,
architecture inventories, and an obsolete Execution Await conformance error
expectation were corrected.

The packaged proof exposed one real UI defect: lineage existed in the side
panel but not in the full-page target view, and a moved source reached by URL
still exposed status and terminal actions. The shared lineage component and
full-page read-only guard fixed that defect before evidence was accepted.

## Packaged product proof record

The current source rebuilt and sealed the isolated bundle at
`/private/tmp/phase10-07-tauri-target/release/bundle/macos/Loom Agents.app`,
including the WebUI resources, all bundled workflows, Loom sidecar, paired
FleetDB sidecar, and Tauri shell. Code-signature validation passed. The Loom
sidecar SHA-256 is
`cf30401c3809c4e819b5b6759068bc5f908bc64534a3cb4596dfb500d6fd6d75`;
the FleetDB sidecar SHA-256 is
`cb0cc8c0216e43ee55e5625cf1efb81f57c1ade68bb1ae8f2a6d89176cba5d5f`.

The run used only
`/private/tmp/phase10-07-desktop-data-20260816-0153`. It created
`PHASE10-07-SOURCE`, `PHASE10-07-TARGET`, source
`PHASE10-07-SOURCE-1`, and target `PHASE10-07-TARGET-1` entirely through the
packaged WebUI. After the first move, the source was Closed and read-only and
the target was Open. The runtime was stopped, the bundle rebuilt with the
full-page fix, and the same data directory restarted on a new dynamic port.
Both records, fields, and links survived. No default local-mode project,
persistent Desktop data, foreign browser profile, or foreign process was
stopped or reused.

Inspected screenshots (PNG, SHA-256):

- `/private/tmp/phase10-07-move-dialog.png` — enabled atomic move intent,
  `a2922c3033815500bec68f09f73776baca3f6cbb86474803986a85e656c92464`;
- `/private/tmp/phase10-07-source-moved.png` — source panel lineage and
  read-only state,
  `3e6efb9bd895de1fcd33c174393905c49dcc63c583b0237bd31e94cd27fb0964`;
- `/private/tmp/phase10-07-target-postrestart.png` — persisted target and
  **Moved from** link,
  `8401f6c8513a9a2b707b76999872b5af6c216ad7b90e30d0655a4bdb5c6dda4f`;
  and
- `/private/tmp/phase10-07-source-postrestart.png` — persisted source,
  disabled status, absent terminal action, **Moved to** link, and explicit
  read-only message,
  `4ea4bc51782c94d4dd441889e84abe458066d8f575fbe07084e66efc9fa94fa3`.

Computer Use was retried against the exact isolated app path after the native
shell launched with the same data and config directories. macOS remained
locked and automatic unlock failed, so this record does not claim a native
window screenshot. The inspected product images come from the sealed bundle's
packaged WebUI and sidecars. Stack 10.12 still owns the final complete native
Desktop matrix.

## Next stack

Stack 10.8 adds the paired FleetDB reviewer-convergence command, then moves
reviewer lifecycle ownership into Agents. It must prove preset drift,
concurrent convergence, creation/archive journeys, transcript attribution,
and retirement of the legacy reviewer path before stack 10.9 starts.
