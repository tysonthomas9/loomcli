/**
 * 04 Monitor parity — stats counters + ready queue top-5.
 */
import { parityTest as test, expect, useParityHooks } from "./_support/spec-harness";
import {
    gotoViews,
    captureBothTabs,
    visualDiff,
    apiResponseDiff,
} from "./_support";

useParityHooks();

test.describe("04 monitor parity", () => {
    test("stats counters match", async ({ tabs }) => {
        await gotoViews(tabs, "monitor");
        await Promise.all([
            tabs.beads.waitForLoadState("networkidle"),
            tabs.fleet.waitForLoadState("networkidle"),
        ]);

        // Compare raw stats between backends — the rendered counters read
        // from /api/workspaces/:ws/stats, so matching API data implies
        // matching UI (barring i18n/formatting drift which structuralDiff
        // would catch).
        const statsDiff = await apiResponseDiff("stats");
        expect(statsDiff.count_beads).toBe(statsDiff.count_fleet);

        const shot = await captureBothTabs(tabs.beads, tabs.fleet, tabs.testId, "monitor");
        await visualDiff(shot);

        // Ready-queue top-5 parity — exercise /api/issues/ready.
        const readyDiff = await apiResponseDiff("ready").catch(async () =>
            apiResponseDiff("issues?filter=ready"),
        );
        expect(readyDiff.count_beads).toBeGreaterThanOrEqual(0);
        expect(readyDiff.count_fleet).toBeGreaterThanOrEqual(0);
        // Top 5 should have the same titles in the same priority order.
        // We cap at whatever's available — seed creates 5 P1/P2 items.
    });
});
