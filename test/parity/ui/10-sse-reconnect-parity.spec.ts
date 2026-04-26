/**
 * 10 SSE reconnect parity — disconnect/reconnect/catch-up symmetry.
 *
 * Spec A: A single observer per backend disconnects (route() abort on the
 * SSE stream URL), a gap mutation is posted via Node fetch during the
 * blackout, the route is restored, and we assert the gap title appears on
 * each observer within 15 s. Latency parity is enforced via timingAssert.
 *
 * Spec B: Three observers per backend (six total). Same flow — abort all,
 * post one gap mutation per backend, restore all, assert every observer
 * sees the gap. Catches hub-fan-out bugs that don't surface with a single
 * subscriber.
 *
 * Container-bounce ("Spec C") is intentionally out of scope per
 * docs/design/sse-reconnect-parity-spec.md (composeRun stop && start is
 * ~10–15s and tests infrastructure reliability rather than parity).
 */
import { parityTest as test, expect, useParityHooks } from "./_support/spec-harness";
import {
    timingAssert,
    discoverWorkspaceId,
    waitForSseReady,
    abortSseRoute,
    restoreSseRoute,
    postIssueViaNode,
    assertCatchupArrived,
    assertNoDuplicates,
} from "./_support";
import { PARITY_URLS } from "./playwright.config";

useParityHooks();

// "Cache invalidation bug" is one of the SEED_FIXTURE child issues. Used
// as the "page-is-loaded" canary before any disconnect simulation. If
// this title fails to render, the seed didn't run or the initial fetch
// pipeline is broken — both should fail loudly here rather than later.
const SEEDED_OPEN_TITLE = "Cache invalidation bug";

// Observers use the /table view rather than /${VIEW}: kanban filters by
// status lane and (per the design caveat) hides issues that don't fit
// the visible columns. Table renders all-status, so a `text=` selector
// can match the gap title regardless of its initial status. This also
// avoids the 09-spec workaround of suppressing waitForSelector failures
// — the table view actually re-renders on a single push event.
const VIEW = "table";

test.describe("10 sse reconnect parity", () => {
    test("catch-up after disconnect/reconnect", async ({ tabs, browser }) => {
        // One observer context per backend. We avoid sharing context with
        // tabs.* (the writer pages) so route() handlers stay scoped to the
        // observer side; the writer-side POST stays unaffected.
        const beadsObsCtx = await browser.newContext();
        const fleetObsCtx = await browser.newContext();
        const beadsObs = await beadsObsCtx.newPage();
        const fleetObs = await fleetObsCtx.newPage();

        try {
            const [beadsWs, fleetWs] = await Promise.all([
                discoverWorkspaceId(PARITY_URLS.beads),
                discoverWorkspaceId(PARITY_URLS.fleet),
            ]);

            await Promise.all([
                beadsObs.goto(`${PARITY_URLS.beads}/ws/${beadsWs}/${VIEW}`),
                fleetObs.goto(`${PARITY_URLS.fleet}/ws/${fleetWs}/${VIEW}`),
            ]);
            // domcontentloaded only — the SPA's polling + open EventSource
            // means networkidle never fires; matches the 09-sse pattern.
            await Promise.all([
                beadsObs.waitForLoadState("domcontentloaded"),
                fleetObs.waitForLoadState("domcontentloaded"),
            ]);

            // Confirm the SSE/initial-fetch pipeline is live before
            // simulating a disconnect — otherwise a missing seed title would
            // be misattributed to the catch-up path failing.
            await Promise.all([
                waitForSseReady(beadsObs, SEEDED_OPEN_TITLE),
                waitForSseReady(fleetObs, SEEDED_OPEN_TITLE),
            ]);

            // ---- Action: disconnect, post gap mutations, reconnect.
            await Promise.all([
                abortSseRoute(beadsObsCtx),
                abortSseRoute(fleetObsCtx),
            ]);

            const ts = Date.now();
            const gapTitleBeads = `gap-beads-${ts}`;
            const gapTitleFleet = `gap-fleet-${ts}`;

            // Node-level POSTs bypass the abort handler, which only filters
            // browser-context traffic. The hub records the mutation and
            // will replay it on the next reconnect.
            await Promise.all([
                postIssueViaNode(PARITY_URLS.beads, beadsWs, gapTitleBeads),
                postIssueViaNode(PARITY_URLS.fleet, fleetWs, gapTitleFleet),
            ]);

            // Restore + start the latency clock at the same moment so
            // beadsMs / fleetMs are directly comparable.
            const reconnectStart = Date.now();
            await Promise.all([
                restoreSseRoute(beadsObsCtx),
                restoreSseRoute(fleetObsCtx),
            ]);

            // ---- Assertions.
            // Each observer must see its own backend's gap title within 15s.
            // assertCatchupArrived returns ms-since-call; we want
            // ms-since-restore, which is exactly the same here because we
            // call assertCatchupArrived immediately after restore.
            const [beadsMs, fleetMs] = await Promise.all([
                assertCatchupArrived(beadsObs, gapTitleBeads),
                assertCatchupArrived(fleetObs, gapTitleFleet),
            ]);
            // Sanity: ensure timings are anchored to the reconnect (within
            // the 15 s waitForSelector budget). Used only for forensics if
            // the run fails — not part of the parity assertion.
            void reconnectStart;

            // No-duplicate invariant: the gap title must appear in the API
            // exactly once per backend. Catches getMutationsSince returning
            // the same event multiple times during catch-up replay.
            await Promise.all([
                assertNoDuplicates(PARITY_URLS.beads, beadsWs, gapTitleBeads),
                assertNoDuplicates(PARITY_URLS.fleet, fleetWs, gapTitleFleet),
            ]);

            // Latency parity: max(beads, fleet) <= 2 * min(beads, fleet),
            // computed by the existing timingAssert helper.
            const t = await timingAssert("sse-reconnect-catchup", {
                beads: beadsMs,
                fleet: fleetMs,
            });
            expect(
                t.within_2x,
                `beads=${beadsMs}ms fleet=${fleetMs}ms`,
            ).toBeTruthy();
        } finally {
            await Promise.allSettled([beadsObsCtx.close(), fleetObsCtx.close()]);
        }
    });

    test("3 simultaneous SSE clients all reconnect cleanly", async ({
        browser,
    }) => {
        // 3 observers per backend, 6 total. Open contexts in parallel to
        // amortize startup cost — the Spec B caveat in the design doc
        // flags this as a measurable budget consideration.
        const N = 3;
        const beadsCtxs = await Promise.all(
            Array.from({ length: N }, () => browser.newContext()),
        );
        const fleetCtxs = await Promise.all(
            Array.from({ length: N }, () => browser.newContext()),
        );
        const allCtxs = [...beadsCtxs, ...fleetCtxs];

        try {
            const beadsPages = await Promise.all(
                beadsCtxs.map((c) => c.newPage()),
            );
            const fleetPages = await Promise.all(
                fleetCtxs.map((c) => c.newPage()),
            );

            const [beadsWs, fleetWs] = await Promise.all([
                discoverWorkspaceId(PARITY_URLS.beads),
                discoverWorkspaceId(PARITY_URLS.fleet),
            ]);

            await Promise.all([
                ...beadsPages.map((p) =>
                    p.goto(`${PARITY_URLS.beads}/ws/${beadsWs}/${VIEW}`),
                ),
                ...fleetPages.map((p) =>
                    p.goto(`${PARITY_URLS.fleet}/ws/${fleetWs}/${VIEW}`),
                ),
            ]);
            await Promise.all(
                [...beadsPages, ...fleetPages].map((p) =>
                    p.waitForLoadState("domcontentloaded"),
                ),
            );

            // Every observer must show the seeded canary before we
            // simulate a disconnect — otherwise we can't tell a missing
            // gap title from a never-connected client.
            await Promise.all(
                [...beadsPages, ...fleetPages].map((p) =>
                    waitForSseReady(p, SEEDED_OPEN_TITLE),
                ),
            );

            // Disconnect all 6 simultaneously.
            await Promise.all(allCtxs.map((c) => abortSseRoute(c)));

            const ts = Date.now();
            const gapTitleBeads = `gap-beads-multi-${ts}`;
            const gapTitleFleet = `gap-fleet-multi-${ts}`;

            await Promise.all([
                postIssueViaNode(PARITY_URLS.beads, beadsWs, gapTitleBeads),
                postIssueViaNode(PARITY_URLS.fleet, fleetWs, gapTitleFleet),
            ]);

            // Restore all 6 at once; this is the "thundering herd"
            // reconnect that the Spec B failure-mode analysis names as
            // the hub-fan-out bug catcher.
            await Promise.all(allCtxs.map((c) => restoreSseRoute(c)));

            // All 3 beads observers see beads gap; all 3 fleet observers
            // see fleet gap. Tracked per side so a per-tab failure
            // surfaces with the slowest tab's latency rather than a
            // total-timeout error.
            const beadsMsList = await Promise.all(
                beadsPages.map((p) => assertCatchupArrived(p, gapTitleBeads)),
            );
            const fleetMsList = await Promise.all(
                fleetPages.map((p) => assertCatchupArrived(p, gapTitleFleet)),
            );

            // No-duplicate per side: the catch-up replay path must not
            // multi-emit the gap mutation under fan-out.
            await Promise.all([
                assertNoDuplicates(PARITY_URLS.beads, beadsWs, gapTitleBeads),
                assertNoDuplicates(PARITY_URLS.fleet, fleetWs, gapTitleFleet),
            ]);

            // Use the slowest observer per side as the parity signal —
            // worst case is what users feel, and a single slow tab
            // signals partial fan-out failure.
            const beadsMs = Math.max(...beadsMsList);
            const fleetMs = Math.max(...fleetMsList);
            const t = await timingAssert("sse-reconnect-catchup-multi", {
                beads: beadsMs,
                fleet: fleetMs,
            });
            expect(
                t.within_2x,
                `beads(max)=${beadsMs}ms fleet(max)=${fleetMs}ms; per-tab beads=[${beadsMsList.join(",")}] fleet=[${fleetMsList.join(",")}]`,
            ).toBeTruthy();
        } finally {
            await Promise.allSettled(allCtxs.map((c) => c.close()));
        }
    });
});
