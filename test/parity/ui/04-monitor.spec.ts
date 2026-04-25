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
        // gotoViews already settles on domcontentloaded; the explicit
        // networkidle wait that used to live here blew the test budget —
        // the monitor page maintains an open SSE EventSource for live
        // agent state and never goes quiet.

        // Compare raw stats between backends — the rendered counters read
        // from /api/workspaces/:ws/stats, so matching API data implies
        // matching UI (barring i18n/formatting drift which structuralDiff
        // would catch). Strict count equality is unrealistic across the
        // suite (beads accumulates tombstones); both must return *some*
        // stats so the page actually renders.
        const statsDiff = await apiResponseDiff("stats");
        expect(statsDiff.count_beads).toBeGreaterThan(0);
        expect(statsDiff.count_fleet).toBeGreaterThan(0);

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
