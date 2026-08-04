/**
 * 10 SSE reconnect fleetdb-regression — disconnect/reconnect/catch-up symmetry.
 *
 * Spec A: A single observer per backend disconnects (route() abort on the
 * SSE stream URL), a gap mutation is posted via Node fetch during the
 * blackout, the route is restored, and we assert the gap title appears on
 * each observer within 15 s. Latency fleetdb-regression is enforced via timingAssert.
 *
 * Spec B: Three observers per backend (six total). Same flow — abort all,
 * post one gap mutation per backend, restore all, assert every observer
 * sees the gap. Catches hub-fan-out bugs that don't surface with a single
 * subscriber.
 *
 * Container-bounce ("Spec C") is intentionally out of scope per
 * docs/design/sse-reconnect-fleetdb-regression-spec.md (composeRun stop && start is
 * ~10–15s and tests infrastructure reliability rather than fleetdb-regression).
 */
import {
  fleetdbTest as test,
  expect,
  useFleetDBHooks,
} from "./_support/spec-harness";
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
import { FLEETDB_URLS } from "./playwright.config";

useFleetDBHooks();

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

test.describe("10 sse reconnect fleetdb-regression", () => {
  test("catch-up after disconnect/reconnect", async ({ tabs, browser }) => {
    // One observer context per backend. We avoid sharing context with
    // tabs.* (the writer pages) so route() handlers stay scoped to the
    // observer side; the writer-side POST stays unaffected.
    const referenceObsCtx = await browser.newContext();
    const fleetObsCtx = await browser.newContext();
    const referenceObs = await referenceObsCtx.newPage();
    const fleetObs = await fleetObsCtx.newPage();

    try {
      const [referenceWs, fleetWs] = await Promise.all([
        discoverWorkspaceId(FLEETDB_URLS.reference),
        discoverWorkspaceId(FLEETDB_URLS.fleet),
      ]);

      await Promise.all([
        referenceObs.goto(`${FLEETDB_URLS.reference}/ws/${referenceWs}/${VIEW}`),
        fleetObs.goto(`${FLEETDB_URLS.fleet}/ws/${fleetWs}/${VIEW}`),
      ]);
      // domcontentloaded only — the SPA's polling + open EventSource
      // means networkidle never fires; matches the 09-sse pattern.
      await Promise.all([
        referenceObs.waitForLoadState("domcontentloaded"),
        fleetObs.waitForLoadState("domcontentloaded"),
      ]);

      // Confirm the SSE/initial-fetch pipeline is live before
      // simulating a disconnect — otherwise a missing seed title would
      // be misattributed to the catch-up path failing.
      await Promise.all([
        waitForSseReady(referenceObs, SEEDED_OPEN_TITLE),
        waitForSseReady(fleetObs, SEEDED_OPEN_TITLE),
      ]);

      // ---- Action: disconnect, post gap mutations, reconnect.
      await Promise.all([
        abortSseRoute(referenceObsCtx),
        abortSseRoute(fleetObsCtx),
      ]);

      const ts = Date.now();
      const gapTitleReference = `gap-reference-${ts}`;
      const gapTitleFleet = `gap-fleet-${ts}`;

      // Node-level POSTs bypass the abort handler, which only filters
      // browser-context traffic. The hub records the mutation and
      // will replay it on the next reconnect.
      await Promise.all([
        postIssueViaNode(FLEETDB_URLS.reference, referenceWs, gapTitleReference),
        postIssueViaNode(FLEETDB_URLS.fleet, fleetWs, gapTitleFleet),
      ]);

      await Promise.all([
        restoreSseRoute(referenceObsCtx),
        restoreSseRoute(fleetObsCtx),
      ]);

      // Each observer must see its own backend's gap title within 15s.
      // assertCatchupArrived starts its clock right after route
      // restoration completes, so referenceMs / fleetMs are directly
      // comparable.
      const [referenceMs, fleetMs] = await Promise.all([
        assertCatchupArrived(referenceObs, gapTitleReference),
        assertCatchupArrived(fleetObs, gapTitleFleet),
      ]);

      // No-duplicate invariant: the gap title must appear in the API
      // exactly once per backend. Catches getMutationsSince returning
      // the same event multiple times during catch-up replay.
      await Promise.all([
        assertNoDuplicates(
          FLEETDB_URLS.reference,
          referenceWs,
          gapTitleReference,
        ),
        assertNoDuplicates(FLEETDB_URLS.fleet, fleetWs, gapTitleFleet),
      ]);

      // Latency regression: max(reference, fleet) <= 2 * min(reference, fleet),
      // computed by the existing timingAssert helper.
      const t = await timingAssert("sse-reconnect-catchup", {
        reference: referenceMs,
        fleet: fleetMs,
      });
      expect(
        t.within_2x,
        `reference=${referenceMs}ms fleet=${fleetMs}ms`,
      ).toBeTruthy();
    } finally {
      await Promise.allSettled([referenceObsCtx.close(), fleetObsCtx.close()]);
    }
  });

  test("3 simultaneous SSE clients all reconnect cleanly", async ({
    browser,
  }) => {
    // 3 observers per backend, 6 total. Open contexts in parallel to
    // amortize startup cost — the Spec B caveat in the design doc
    // flags this as a measurable budget consideration.
    const N = 3;
    const referenceCtxs = await Promise.all(
      Array.from({ length: N }, () => browser.newContext()),
    );
    const fleetCtxs = await Promise.all(
      Array.from({ length: N }, () => browser.newContext()),
    );
    const allCtxs = [...referenceCtxs, ...fleetCtxs];

    try {
      const referencePages = await Promise.all(
        referenceCtxs.map((c) => c.newPage()),
      );
      const fleetPages = await Promise.all(fleetCtxs.map((c) => c.newPage()));

      const [referenceWs, fleetWs] = await Promise.all([
        discoverWorkspaceId(FLEETDB_URLS.reference),
        discoverWorkspaceId(FLEETDB_URLS.fleet),
      ]);

      await Promise.all([
        ...referencePages.map((p) =>
          p.goto(`${FLEETDB_URLS.reference}/ws/${referenceWs}/${VIEW}`),
        ),
        ...fleetPages.map((p) =>
          p.goto(`${FLEETDB_URLS.fleet}/ws/${fleetWs}/${VIEW}`),
        ),
      ]);
      await Promise.all(
        [...referencePages, ...fleetPages].map((p) =>
          p.waitForLoadState("domcontentloaded"),
        ),
      );

      // Every observer must show the seeded canary before we
      // simulate a disconnect — otherwise we can't tell a missing
      // gap title from a never-connected client.
      await Promise.all(
        [...referencePages, ...fleetPages].map((p) =>
          waitForSseReady(p, SEEDED_OPEN_TITLE),
        ),
      );

      // Disconnect all 6 simultaneously.
      await Promise.all(allCtxs.map((c) => abortSseRoute(c)));

      const ts = Date.now();
      const gapTitleReference = `gap-reference-multi-${ts}`;
      const gapTitleFleet = `gap-fleet-multi-${ts}`;

      await Promise.all([
        postIssueViaNode(FLEETDB_URLS.reference, referenceWs, gapTitleReference),
        postIssueViaNode(FLEETDB_URLS.fleet, fleetWs, gapTitleFleet),
      ]);

      // Restore all 6 at once; this is the "thundering herd"
      // reconnect that the Spec B failure-mode analysis names as
      // the hub-fan-out bug catcher.
      await Promise.all(allCtxs.map((c) => restoreSseRoute(c)));

      // All 3 reference observers see reference gap; all 3 fleet observers
      // see fleet gap. Tracked per side so a per-tab failure
      // surfaces with the slowest tab's latency rather than a
      // total-timeout error.
      const referenceMsList = await Promise.all(
        referencePages.map((p) => assertCatchupArrived(p, gapTitleReference)),
      );
      const fleetMsList = await Promise.all(
        fleetPages.map((p) => assertCatchupArrived(p, gapTitleFleet)),
      );

      // No-duplicate per side: the catch-up replay path must not
      // multi-emit the gap mutation under fan-out.
      await Promise.all([
        assertNoDuplicates(
          FLEETDB_URLS.reference,
          referenceWs,
          gapTitleReference,
        ),
        assertNoDuplicates(FLEETDB_URLS.fleet, fleetWs, gapTitleFleet),
      ]);

      // Use the slowest observer per side as the fleetdb-regression signal —
      // worst case is what users feel, and a single slow tab
      // signals partial fan-out failure.
      const referenceMs = Math.max(...referenceMsList);
      const fleetMs = Math.max(...fleetMsList);
      const t = await timingAssert("sse-reconnect-catchup-multi", {
        reference: referenceMs,
        fleet: fleetMs,
      });
      expect(
        t.within_2x,
        `reference(max)=${referenceMs}ms fleet(max)=${fleetMs}ms; per-tab reference=[${referenceMsList.join(",")}] fleet=[${fleetMsList.join(",")}]`,
      ).toBeTruthy();
    } finally {
      await Promise.allSettled(allCtxs.map((c) => c.close()));
    }
  });
});
