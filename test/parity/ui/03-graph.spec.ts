/**
 * 03 Graph parity — node + edge count.
 */
import { parityTest as test, expect, useParityHooks } from "./_support/spec-harness";
import {
    gotoViews,
    captureBothTabs,
    visualDiff,
    apiResponseDiff,
} from "./_support";

useParityHooks();

test.describe("03 graph parity", () => {
    test("same node + edge count", async ({ tabs }) => {
        await gotoViews(tabs, "graph");

        // React Flow renders nodes inside .react-flow__node.
        const node = ".react-flow__node";
        const edge = ".react-flow__edge";
        await Promise.all([
            tabs.beads.waitForSelector(node, { timeout: 20_000 }).catch(() => undefined),
            tabs.fleet.waitForSelector(node, { timeout: 20_000 }).catch(() => undefined),
        ]);

        const [bNodes, fNodes, bEdges, fEdges] = await Promise.all([
            tabs.beads.locator(node).count(),
            tabs.fleet.locator(node).count(),
            tabs.beads.locator(edge).count(),
            tabs.fleet.locator(edge).count(),
        ]);

        expect(bNodes).toBeGreaterThan(0);
        expect(fNodes).toBeGreaterThan(0);
        expect(Math.abs(bNodes - fNodes)).toBeLessThanOrEqual(1);
        // Seed doesn't add explicit edges; both sides should render the
        // same parent-child edges from epics.
        expect(Math.abs(bEdges - fEdges)).toBeLessThanOrEqual(1);

        const shot = await captureBothTabs(tabs.beads, tabs.fleet, tabs.testId, "graph-default");
        await visualDiff(shot, 0.05); // Graph layout has more cosmetic drift; allow 5%.

        const apiDiff = await apiResponseDiff("issues/graph").catch(async () =>
            apiResponseDiff("issues"),
        );
        expect(apiDiff.count_beads).toBeGreaterThan(0);
    });
});
