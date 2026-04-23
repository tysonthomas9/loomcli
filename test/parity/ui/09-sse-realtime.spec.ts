/**
 * 09 SSE / realtime parity — create in tab1 reaches tab2 within 2s.
 *
 * Opens THREE pages: the standard dual tabs (for parity) plus a second
 * fleet tab to observe the SSE push. We measure the delta between
 * POST and the second tab's DOM update on each backend and assert that
 * fleet's latency is within 2× of beads'.
 */
import { parityTest as test, expect, useParityHooks } from "./_support/spec-harness";
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
        const beadsObsCtx = await browser.newContext();
        const fleetObsCtx = await browser.newContext();
        const beadsObs = await beadsObsCtx.newPage();
        const fleetObs = await fleetObsCtx.newPage();
        const [beadsWs, fleetWs] = await Promise.all([
            discoverWorkspaceId(PARITY_URLS.beads),
            discoverWorkspaceId(PARITY_URLS.fleet),
        ]);
        await Promise.all([
            beadsObs.goto(`${PARITY_URLS.beads}/ws/${beadsWs}/kanban`),
            fleetObs.goto(`${PARITY_URLS.fleet}/ws/${fleetWs}/kanban`),
            // The writer tabs (tabs.beads/tabs.fleet from the DualTabs
            // fixture) default to about:blank. The `evaluate` block below
            // reads the workspace ID out of location.pathname, so they
            // need to be on /ws/{id}/kanban too — otherwise the URL split
            // returns an empty string and the fetch never fires.
            tabs.beads.goto(`${PARITY_URLS.beads}/ws/${beadsWs}/kanban`),
            tabs.fleet.goto(`${PARITY_URLS.fleet}/ws/${fleetWs}/kanban`),
        ]);
        await Promise.all([
            beadsObs.waitForLoadState("networkidle"),
            fleetObs.waitForLoadState("networkidle"),
            tabs.beads.waitForLoadState("networkidle"),
            tabs.fleet.waitForLoadState("networkidle"),
        ]);

        try {
            const beadsTitle = `parity-sse-beads-${Date.now()}`;
            const fleetTitle = `parity-sse-fleet-${Date.now()}`;

            // Create on beads side (via the beads tab) and time its arrival
            // at the beads observer.
            const beadsStart = Date.now();
            await tabs.beads.evaluate(
                async ({ title }) => {
                    // URL is /ws/<uuid>/kanban after the goto above — read
                    // the UUID out of the path, not a hardcoded "default".
                    const ws = new URL(location.href).pathname.split("/")[2] ?? "";
                    if (!ws) throw new Error("could not extract ws id from " + location.href);
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
                { title: beadsTitle },
            );
            await beadsObs
                .waitForSelector(`text=${beadsTitle}`, { timeout: 5000 })
                .catch(() => undefined);
            const beadsMs = Date.now() - beadsStart;

            // Same on fleet side, wrapped in routing assertion.
            const fleetStart = Date.now();
            await assertRoutingForAction(tabs.testId, "sse-create", fleetSpy, async () => {
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
            });
            await fleetObs
                .waitForSelector(`text=${fleetTitle}`, { timeout: 5000 })
                .catch(() => undefined);
            const fleetMs = Date.now() - fleetStart;

            const t = await timingAssert("sse-propagation", {
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
});
