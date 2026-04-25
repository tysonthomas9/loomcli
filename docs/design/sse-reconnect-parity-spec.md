# SSE reconnect parity spec design

## Problem

`09-sse-realtime.spec.ts` proves "event in tab1 reaches tab2 within 2× of
other backend" but doesn't test:
- EventSource reconnection after a network drop
- State catch-up after reconnect (mutations missed during the gap)
- Backend symmetry of reconnection latency / catch-up window
- Multiple simultaneous SSE clients all reconnecting cleanly
- Behavior when the SSE backend itself is restarted

## Decision

**Two test blocks in one new file: `10-sse-reconnect-parity.spec.ts`.**

- Spec A: disconnect → reconnect → catch-up (happy path)
- Spec B: three simultaneous SSE clients all reconnect

Container-bounce ("Spec C") is excluded from the initial implementation
because `composeRun stop && start` is ~10–15 s and tests infrastructure
reliability rather than the parity surface. Add as `11-sse-container-bounce.spec.ts`
later when the team budgets the extra time.

## Disconnect simulation: `page.route()` abort

| Option | Verdict |
|---|---|
| `setOffline(true)` | Cuts ALL requests — can't POST mutations during the gap from the same context |
| Container restart | Too slow (~15s); fragile; tests infra not parity |
| `route()` abort on SSE URL | **Chosen** — surgical, deterministic, leaves POSTs unaffected |
| `route()` fulfill with hung response | `EventSource.onerror` may not fire on hang in all browsers — flaky |

Implementation: `ctx.route("**/workspaces/*/events*", route => route.abort())`
to disconnect; `ctx.unroute("**/workspaces/*/events*")` to restore.

Note the URL pattern: `**/workspaces/*/events*` matches the SSE stream
endpoint, not `**/events/token` (the auth-token bootstrap), so the
catch-up path can re-fetch its token after reconnect.

## Reconnect-success and catch-up correctness assertions

The SPA's SSE client (`internal/webui/frontend/src/api/common/sse.ts`)
uses `EventSource` with manual reconnect: exponential backoff
`min(1000 * 2^(attempts-1), 30000)` ms, stores `lastEventId` as
unix-ms integer, reconnects with `?since=<lastEventId>` (NOT
`Last-Event-ID` header — the BeadsSSEClient builds the URL itself).

The server's `event: connected` fires AFTER catch-up replay completes
(`handler.go:103-108`), which is the canonical "reconnect succeeded"
signal. But asserting against the SPA's `onConnected` handler couples
to internal SPA state. Use a second-order proof instead:

**Reconnect success assertion**: post a "canary" issue via Node `fetch`
after `unroute`; assert the canary title appears in the observer tab's
DOM via `page.waitForSelector(\`text=${title}\`, { timeout: 15_000 })`.
If the canary appears, the SSE subscription is live (push delivered) AND
catch-up succeeded.

**Catch-up correctness assertion**:
1. Capture issue count N via Node fetch
2. Abort SSE route on both observer contexts
3. POST M=2 distinct gap-mutations via Node fetch (titles like `gap-{backend}-{ts}`)
4. Unroute (restore SSE)
5. Assert both observer tabs show all M new titles within 15s
6. Fetch issue list: assert count = N + M, each gap title appears exactly once

**No-duplicate assertion**: after catch-up, `filter(title === gapTitle).length === 1`. Catches a backend bug where `getMutationsSince` returns the same event multiple times.

**Latency parity**: time from `unroute()` to canary appearance per backend; assert `max(beadsMs, fleetMs) <= 2 * min(beadsMs, fleetMs)` via the existing `timingAssert` helper.

## Spec A: Disconnect → Reconnect → Catch-up (~110 LOC)

```
useParityHooks();
test.describe("10 sse reconnect parity", () => {
    test("catch-up after disconnect/reconnect", async ({ tabs, browser }) => {
        // Open extra observer contexts (mirrors 09-sse pattern)
        // Discover workspace IDs
        // Navigate writer + observer tabs to /ws/{id}/kanban; wait for
        //   domcontentloaded
        // assertSseLive(observers) — wait for a known seed title to render
        //
        // Action:
        //   abortSseRoute(beadsObsCtx); abortSseRoute(fleetObsCtx)
        //   const gapTitleBeads = `gap-beads-${Date.now()}`
        //   const gapTitleFleet = `gap-fleet-${Date.now()}`
        //   await postIssueViaNode(BEADS, beadsWs, gapTitleBeads)
        //   await postIssueViaNode(FLEET, fleetWs, gapTitleFleet)
        //   const reconnectStart = Date.now()
        //   restoreSseRoute(beadsObsCtx); restoreSseRoute(fleetObsCtx)
        //
        // Assertions:
        //   const beadsMs = await assertCatchupArrived(beadsObs, gapTitleBeads)
        //   const fleetMs = await assertCatchupArrived(fleetObs, gapTitleFleet)
        //   await assertNoDuplicates(BEADS, beadsWs, gapTitleBeads)
        //   await assertNoDuplicates(FLEET, fleetWs, gapTitleFleet)
        //   const t = await timingAssert("sse-reconnect-catchup",
        //       { beads: beadsMs, fleet: fleetMs })
        //   expect(t.within_2x).toBeTruthy()
    });
});
```

## Spec B: Three Simultaneous SSE Clients (~90 LOC)

Same shape as Spec A but opens 3 observer pages per backend (6 total).
Aborts SSE on all 6 simultaneously, posts gap mutation, restores all 6,
asserts all 3 beads observers + all 3 fleet observers see the gap mutation
+ canary. Catches hub-fan-out bugs and per-client buffer overflow.

## Helpers in new `_support/sse-helpers.ts` (~100 LOC)

```typescript
// Wait for SSE to be live by checking that a known title is rendered.
waitForSseReady(page: Page, knownTitle: string, timeout = 10_000): Promise<void>

// Abort the SSE stream route on this context (idempotent).
abortSseRoute(ctx: BrowserContext): Promise<void>

// Restore the SSE stream route on this context (idempotent).
restoreSseRoute(ctx: BrowserContext): Promise<void>

// POST an issue via Node fetch (bypasses browser tab routing).
// Returns the new issue ID.
postIssueViaNode(baseUrl: string, wsId: string, title: string): Promise<string>

// Wait for a title to appear in the observer DOM; return ms elapsed.
assertCatchupArrived(obs: Page, title: string, timeoutMs = 15_000): Promise<number>

// Fetch issue list from Node; assert title appears exactly once.
assertNoDuplicates(baseUrl: string, wsId: string, title: string): Promise<void>
```

## Failure modes (bugs this catches)

1. **Fleet ignores `?since`** — catch-up never fires, gap mutations don't
   appear on fleet observer. Fails on `waitForSelector(gapTitleFleet)`.
2. **Fleet `getMutationsSince` returns full history** — duplicates
   appear. Fails `assertNoDuplicates`.
3. **Fleet reconnect 3× slower** — `timingAssert` 2× threshold fails.
4. **Hub buffer overflow on simultaneous reconnect** (Spec B) — gap
   mutation dropped on some observers. Fails `waitForSelector` on
   affected tab.
5. **`sendCatchUp` called but `getMutationsSince` is nil** — mutations
   never replayed. Fails `waitForSelector(gapTitle)`.

## False-positive risks

- **Token-fetch URL collision**: SSE token is `**/events/token`. Use
  `**/workspaces/*/events*` (specific to the streaming endpoint) to
  avoid matching the token endpoint.
- **Status-filtered kanban hides issues**: kanban filters by status; if
  gap issues land in an off-screen lane, `waitForSelector` may miss
  them. Use the table view (shows all status), or seed canary issues
  as `open` (default lane shown).
- **Partial-text selector matches wrong issue**: titles must be unique
  enough — UUID suffix or `Date.now()` timestamps are sufficient.
- **Spec B resource cost**: 6 extra pages per test. Open in
  `Promise.all` to amortize startup.
- **`page.unroute` async window**: BeadsSSEClient backoff is 1 s
  minimum, plenty of time for the unroute to complete before the next
  reconnect attempt.

## Build sequence

1. Add `sse-helpers.ts` to `_support/` with the 6 helpers; export from `_support/index.ts`
2. Add Spec A as first `test()` in `10-sse-reconnect-parity.spec.ts`
3. Run Spec A alone — verify it passes on beads first, then fleet
4. Add Spec B as second `test()`
5. Run full file — verify both pass
6. Run full suite — confirm total budget remains under 30 min

## LOC

- `_support/sse-helpers.ts` — ~100
- `10-sse-reconnect-parity.spec.ts` — ~200 (A: ~110, B: ~90)
- **Total: ~300 LOC**

Spec 09 for comparison is 121 LOC.
