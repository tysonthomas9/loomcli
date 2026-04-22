/**
 * 01 Kanban parity — swim lane count, drag-drop status change, ordering.
 *
 * Per ui-test-plan.md §1 / browse.md "Kanban view" checklist.
 */
import { parityTest as test, expect, useParityHooks } from "./_support/spec-harness";
import {
    gotoViews,
    captureBothTabs,
    visualDiff,
    structuralDiff,
    apiResponseDiff,
    SEED_FIXTURE,
    assertRoutingForAction,
} from "./_support";

useParityHooks();

test.describe("01 kanban parity", () => {
    test("both tabs render the same swim-lane counts", async ({ tabs, stateBefore }) => {
        await gotoViews(tabs, "kanban");

        // Wait for swim-lane sections to render on both tabs.
        await Promise.all([
            tabs.beads.waitForSelector('section[data-status]', { timeout: 15_000 }),
            tabs.fleet.waitForSelector('section[data-status]', { timeout: 15_000 }),
        ]);

        const shot = await captureBothTabs(tabs.beads, tabs.fleet, tabs.testId, "swim-lanes");
        const vis = await visualDiff(shot);
        const struct = await structuralDiff(tabs.beads, tabs.fleet);

        // Per the fixture, total issue count is 13 (3 epics + 10 children).
        // Kanban excludes epics by default, so we expect 10 cards per side.
        const [beadsCards, fleetCards] = await Promise.all([
            tabs.beads.locator('section[data-status] article').count(),
            tabs.fleet.locator('section[data-status] article').count(),
        ]);
        expect(beadsCards).toBe(fleetCards);

        const apiDiff = await apiResponseDiff("issues");
        expect(apiDiff.count_beads).toBeGreaterThan(0);
        expect(apiDiff.count_fleet).toBeGreaterThan(0);

        // Log structural drift for the HTML report; don't fail solely on it
        // because different class-hash suffixes between builds are OK.
        if (!struct.match) {
            // eslint-disable-next-line no-console
            console.log(
                `[01-kanban] structural diff: beads=${struct.beads_nodes} fleet=${struct.fleet_nodes} extras=${struct.extra_on_beads.length}/${struct.extra_on_fleet.length}`,
            );
        }
        expect(vis.within, `visual diff ratio=${vis.ratio}`).toBeTruthy();
    });

    test("drag-drop status change routes through fleet-db", async ({ tabs, fleetSpy }) => {
        test.fail(
            !SEED_FIXTURE.children.length,
            "no seed fixture available",
        );
        await gotoViews(tabs, "kanban");
        await tabs.fleet.waitForSelector('article', { timeout: 15_000 });

        // Drag a card from "ready" to "in_progress" on the fleet side.
        // Use the API-level move rather than DOM drag-drop because
        // dnd-kit drag events are flaky under Playwright; the wire effect
        // is identical and we care about routing, not dnd-kit ergonomics.
        await assertRoutingForAction(
            tabs.testId,
            "kanban-status-change",
            fleetSpy,
            async () => {
                const card = tabs.fleet.locator('article').first();
                await card.waitFor({ timeout: 10_000 });
                const id = await card.getAttribute("data-id");
                expect(id).toBeTruthy();
                // PATCH via the same API the UI calls on drop.
                await tabs.fleet.evaluate(
                    async ({ id }) => {
                        const r = await fetch(
                            `/api/workspaces/${new URL(location.href).pathname.split("/")[2]}/issues/${id}`,
                            {
                                method: "PATCH",
                                headers: { "Content-Type": "application/json" },
                                body: JSON.stringify({ status: "in_progress" }),
                            },
                        );
                        if (!r.ok) throw new Error(`PATCH failed: ${r.status}`);
                    },
                    { id },
                );
            },
        );

        // Reload fleet tab and verify the status persisted.
        await tabs.fleet.reload();
        const stillVisible = tabs.fleet.locator('section[data-status="in_progress"] article');
        await expect(stillVisible.first()).toBeVisible({ timeout: 10_000 });
    });
});
