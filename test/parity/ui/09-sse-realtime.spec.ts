/**
 * 09 SSE / realtime parity — create in tab1 reaches tab2 within 2s.
 *
 * Opens THREE pages: the standard dual tabs (for parity) plus a second
 * fleet tab to observe the SSE push. We measure the delta between
 * POST and the second tab's DOM update on each backend and assert that
 * fleet's latency is within 2× of reference'.
 */
import {
  parityTest as test,
  expect,
  useParityHooks,
} from "./_support/spec-harness";
import {
  timingAssert,
  assertRoutingForAction,
  discoverWorkspaceId,
} from "./_support";
import { PARITY_URLS } from "./playwright.config";

useParityHooks();

test.describe("09 SSE realtime parity", () => {
  test("event propagates to second tab within 2x of other side", async ({
    tabs,
    browser,
    fleetSpy,
  }) => {
    // Second observer tabs on each backend, same browser for realism.
    const referenceObsCtx = await browser.newContext();
    const fleetObsCtx = await browser.newContext();
    const referenceObs = await referenceObsCtx.newPage();
    const fleetObs = await fleetObsCtx.newPage();
    const [referenceWs, fleetWs] = await Promise.all([
      discoverWorkspaceId(PARITY_URLS.reference),
      discoverWorkspaceId(PARITY_URLS.fleet),
    ]);
    await Promise.all([
      referenceObs.goto(`${PARITY_URLS.reference}/ws/${referenceWs}/kanban`),
      fleetObs.goto(`${PARITY_URLS.fleet}/ws/${fleetWs}/kanban`),
      // The writer tabs (tabs.reference/tabs.fleet from the DualTabs
      // fixture) default to about:blank. The `evaluate` block below
      // reads the workspace ID out of location.pathname, so they
      // need to be on /ws/{id}/kanban too — otherwise the URL split
      // returns an empty string and the fetch never fires.
      tabs.reference.goto(`${PARITY_URLS.reference}/ws/${referenceWs}/kanban`),
      tabs.fleet.goto(`${PARITY_URLS.fleet}/ws/${fleetWs}/kanban`),
    ]);
    // Only the observer tabs need networkidle — they wait for the SSE
    // subscription to be established before we expect them to see the
    // title text. The writer tabs (tabs.reference/tabs.fleet) only need
    // location.href set; the SPA's ongoing polling means networkidle
    // never fires, and waiting for it would blow the 30s budget.
    await Promise.all([
      referenceObs.waitForLoadState("domcontentloaded"),
      fleetObs.waitForLoadState("domcontentloaded"),
    ]);

    try {
      const referenceTitle = `parity-sse-reference-${Date.now()}`;
      const fleetTitle = `parity-sse-fleet-${Date.now()}`;

      // Create on reference side (via the reference tab) and time its arrival
      // at the reference observer.
      const referenceStart = Date.now();
      await tabs.reference.evaluate(
        async ({ title }) => {
          // URL is /ws/<uuid>/kanban after the goto above — read
          // the UUID out of the path, not a hardcoded "default".
          const ws = new URL(location.href).pathname.split("/")[2] ?? "";
          if (!ws)
            throw new Error("could not extract ws id from " + location.href);
          await fetch(`/api/workspaces/${ws}/issues`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              title,
              issue_type: "task",
              priority: 3,
            }),
          });
        },
        { title: referenceTitle },
      );
      await referenceObs
        .waitForSelector(`text=${referenceTitle}`, { timeout: 5000 })
        .catch(() => undefined);
      const referenceMs = Date.now() - referenceStart;

      // Same on fleet side, wrapped in routing assertion.
      const fleetStart = Date.now();
      await assertRoutingForAction(
        tabs.testId,
        "sse-create",
        fleetSpy,
        async () => {
          await tabs.fleet.evaluate(
            async ({ title, ws }) => {
              await fetch(`/api/workspaces/${ws}/issues`, {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({
                  title,
                  issue_type: "task",
                  priority: 3,
                }),
              });
            },
            { title: fleetTitle, ws: fleetWs },
          );
        },
      );
      await fleetObs
        .waitForSelector(`text=${fleetTitle}`, { timeout: 5000 })
        .catch(() => undefined);
      const fleetMs = Date.now() - fleetStart;

      const t = await timingAssert("sse-propagation", {
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
});
