# SSE subscriber backend fallback (fleet mode)

## Problem

In **fleet mode** the bd daemon is intentionally absent, so the
`DaemonSubscriber` never activates → browsers see `connecting…`
forever and the realtime push channel is dead. Tests appear to pass
only because the SPA polls REST endpoints to fetch new state.

The `IssueBackend` interface already exposes the right primitives:
- `GetMutations(ctx, since, limit)` — pull pending mutations since cursor
- `WaitForMutations(ctx, since, timeout)` — long-poll for new mutations

Both beads and fleet adapters implement them. The work is to bridge
`IssueBackend.WaitForMutations` into the existing SSE hub so any backend
with mutation support can push events.

## Decision

**`MutationSource`-shaped interface local to `multi.go` + a new
`BackendMutationSubscriber` concrete type, with runtime selection.**

Beads and fleet share the same `realtime.Hub` and emit identical
`MutationPayload` bytes. The only difference is how mutations are
sourced. A small interface lets `MultiWorkspaceSubscriber` host both
subscriber types without changing the hub or SSE handler. Parallel
concrete types selected per-workspace at activation time keep the
beads path entirely unchanged and avoid build-time mode flags bleeding
into shared code.

The alternative — selecting at build time via top-level `if fleetMode`
in `server_app.go` — would duplicate the multi-workspace fan-out logic
and make it impossible to serve mixed beads/fleet workspaces in a single
process.

## Components

### New: `internal/webui/server/realtime/backend_adapter.go` (~45 LOC)
- `BackendMutationToPayload(m backend.MutationData, workspaceID string) *MutationPayload`
- `BackendMutationToRPCEvent(m backend.MutationData) rpc.MutationEvent` — for the catch-up path projection
- `RPCEventToMutationData(e rpc.MutationEvent) backend.MutationData` — for the unified `workspaceSubscriber` interface return type

### New: `internal/webui/subscription/backend_subscriber.go` (~120 LOC)
`BackendMutationSubscriber` — sibling of `DaemonSubscriber`, sources from
`backend.IssueBackend.WaitForMutations`. Fields: `backend`, `hub`,
`workspaceID`, `done`, `wg`, `lastSince`, `mu`, `startOnce`, `stopOnce`,
`ctx`, `cancel`. Public surface: `Start()`, `Stop()`,
`GetMutationsSince(since int64) []backend.MutationData`.

Loop: one goroutine, calls `WaitForMutations(ctx, lastSince, 30s)`;
update `lastSince` (max timestamp) before broadcasting; 2 s backoff on
non-cancellation errors; clean exit on `done` close or `ctx.Done()`.

### New: `internal/webui/hooks/fleet_subscriber_hook.go` (~80 LOC)
`coordinator.LifecycleHook` that reads `*fleet.FleetBackend` from the
resource bag and calls `multiSub.AddWorkspaceWithBackend` on `Activate`.
Implements `subscriberActivator` — the existing deferred-activation
pattern. `OnRegister` is no-op. `OnDeregister` calls `RemoveWorkspace`.

### Modified: `internal/webui/subscription/multi.go` (+80 LOC)
- Add local `workspaceSubscriber` interface: `Start()`, `Stop()`,
  `GetMutationsSince(since int64) []backend.MutationData`
- `subscriberEntry.sub` becomes `workspaceSubscriber`
- New `AddWorkspaceWithBackend(wsID, b)` mirrors `AddWorkspace`
- `GetMutationsSinceForWorkspace` projects through `BackendMutationToRPCEvent`

### Modified: `internal/webui/subscription/subscriber.go` (+15 LOC)
- Add `GetMutationsSince(since int64) []backend.MutationData` adapter
  method on `DaemonSubscriber` so it satisfies the unified interface

### Modified: `internal/webui/appinfra/appinfra.go` (+15 LOC)
- In the `cfg.FleetMode` branch of `RegisterHooks`, register
  `FleetSubscriberHook(multiSub, registry, logger)`

No change in `realtime/handler.go`, `app/server_app.go`,
`coordinator/registry.go`, or `internal/backend/fleet/fleet.go`
(everything needed already exists).

## Data flow (fleet push, new path)

1. Browser opens `GET /api/workspaces/{ws}/events`
2. `ActivateSubscriber(wsID)` → `FleetSubscriberHook.Activate(wsID)` (lazy, idempotent)
3. Activate reads `coordinator.ResourceKeyFleetBackend` → `*fleet.FleetBackend`
4. `multiSub.AddWorkspaceWithBackend(wsID, fb)` creates `BackendMutationSubscriber` and starts its goroutine
5. Goroutine: `fb.WaitForMutations(ctx, lastSince, 30s)` → HTTP GET `/events/mutations?since=N&timeout=30000`
6. On non-empty: update `lastSince` (max ts), per item call `BackendMutationToPayload(m, wsID)` → `hub.Broadcast`
7. Hub fans out to clients with matching `workspaceID`
8. Browser EventSource receives `event: mutation\ndata: {...}\n\n`
9. On timeout (empty): loop immediately
10. On Stop(): cancel ctx, close done, exit cleanly

## Catch-up (reconnecting client)

1. Browser reconnects with `?since=N` (or `Last-Event-ID: N`)
2. `handler.sendCatchUp` → `multiSub.GetMutationsSinceForWorkspace(wsID, N)`
3. Multi finds `BackendMutationSubscriber` → calls `GetMutationsSince(N)` → `fb.GetMutations(ctx, N)` (non-blocking)
4. Project each `backend.MutationData` → `rpc.MutationEvent` → `MutationPayload`
5. `sendCatchUp` writes SSE events

## Build sequence

1. **Foundation** (no behavior change): adapter functions in `realtime/`
2. **New subscriber type**: `BackendMutationSubscriber` + unit tests; widen `multi.go` interface; add `DaemonSubscriber` adapter
3. **Hook wiring**: `FleetSubscriberHook` + register in `appinfra.go`
4. **Integration verification**: existing `subscriber_test.go` and
   `fleet_test.go` pass unchanged; `09-sse-realtime` shows live push
   without manual navigation

## Test plan

Existing tests must keep passing: `subscription/subscriber_test.go`,
`backend/fleet/fleet_test.go`, `coordinator/registry_test.go`.

New unit tests:
- `subscription/backend_subscriber_test.go` (~150 LOC): start/stop, broadcast, lastSince advance, retry, GetMutationsSince, ctx-cancel-on-stop
- `realtime/backend_adapter_test.go` (~60 LOC): field projection (esp. RFC3339 timestamp + WorkspaceID injection), round-trip equivalence with `RPCMutationToPayload`
- `hooks/fleet_subscriber_hook_test.go` (~60 LOC): Activate, idempotent, OnDeregister
- `subscription/multi_test.go` additions (~40 LOC)

Integration: the existing `09-sse-realtime` spec already validates push
delivery; once landed, the 5-second polling fallback in the test
becomes redundant (push delivers in <500ms typical).

## Risks

**Cursor drift under concurrent activate** — close TOCTOU in
`AddWorkspaceWithBackend` with the existing `mu.Lock()` pattern.

**Cursor precision loss (fleet-0qcs)** — fleet-db cursor is
`<ms>-<seq>`; `WaitForMutations` accepts/returns `int64` ms epochs.
Two events in the same ms → next call re-receives them. Browser
deduplicates via rendered state. Acceptable for current milestone.

**Backpressure** — `hub.Broadcast` has 1024-item retry queue + drops
on overflow. Fleet subscriber produces ≤ one broadcast per
`WaitForMutations` return; volume stays low.

**Multi-workspace fan-out** — N workspaces = N goroutines, each
holding one open HTTP connection for 30s. Fine for `loom serve`
(few workspaces). Multi-tenant with hundreds of workspaces would
need single-connection multiplexing — deferred.

**Context leak on Stop()** — cancel ctx first (unblocks long-poll),
then `wg.Wait()`, then close `done`. Pattern is in `subscription/`'s
existing `waitWithDone` helper.

**Wire format stability** — `BackendMutationToPayload` and
`RPCMutationToPayload` MUST produce byte-identical JSON. Both use
`time.RFC3339`. Test asserts string equality on a shared fixture.

## LOC summary

| File | New | Modified |
|------|-----|----------|
| `realtime/backend_adapter.go` | 45 | — |
| `subscription/backend_subscriber.go` | 120 | — |
| `hooks/fleet_subscriber_hook.go` | 80 | — |
| `subscription/multi.go` | — | +80 |
| `subscription/subscriber.go` | — | +15 |
| `appinfra/appinfra.go` | — | +15 |
| Test files (3 new + 1 additions) | ~310 | — |
| **Total** | **555** | **110** |

Production code: ~355 LOC across 3 new + 3 modified files.
