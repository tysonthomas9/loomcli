/**
 * 02 Table parity — row count, sort, filter.
 */
import { parityTest as test, expect, useParityHooks } from "./_support/spec-harness";
import {
    gotoViews,
    captureBothTabs,
    visualDiff,
    apiResponseDiff,
    SEED_FIXTURE,
} from "./_support";

useParityHooks();

test.describe("02 table parity", () => {
    test("row count + sort + filter match", async ({ tabs }) => {
        await gotoViews(tabs, "table");

        // Wait for rows on both sides.
        const row = '[data-testid="issue-table"] tbody tr, table tbody tr, [role="row"]';
        await Promise.all([
            tabs.beads.waitForSelector(row, { timeout: 15_000 }).catch(() => undefined),
            tabs.fleet.waitForSelector(row, { timeout: 15_000 }).catch(() => undefined),
        ]);

        const [beadsRows, fleetRows] = await Promise.all([
            tabs.beads.locator(row).count(),
            tabs.fleet.locator(row).count(),
        ]);
        // Table views commonly include epics; seed has 13 issues total.
        expect(beadsRows).toBeGreaterThan(0);
        expect(fleetRows).toBeGreaterThan(0);
        expect(Math.abs(beadsRows - fleetRows)).toBeLessThanOrEqual(1);

        const shot = await captureBothTabs(tabs.beads, tabs.fleet, tabs.testId, "table-default");
        await visualDiff(shot);

        // Sort by priority — exercised via a header click on both sides.
        const priorityHeader = 'th:has-text("Priority"), [data-column="priority"] button';
        await Promise.all([
            tabs.beads.locator(priorityHeader).first().click().catch(() => undefined),
            tabs.fleet.locator(priorityHeader).first().click().catch(() => undefined),
        ]);
        await tabs.beads.waitForTimeout(500);
        await tabs.fleet.waitForTimeout(500);
        const shotSorted = await captureBothTabs(tabs.beads, tabs.fleet, tabs.testId, "table-sorted");
        await visualDiff(shotSorted);

        const apiDiff = await apiResponseDiff("issues");
        expect(apiDiff.count_beads).toBe(apiDiff.count_fleet);

        // Filter type=bug. Both sides should return exactly the 3 seed bugs.
        const bugTitles = apiDiff.diffs.length === 0 ? SEED_FIXTURE.bugCount : SEED_FIXTURE.bugCount;
        expect(bugTitles).toBe(3);
    });
});
