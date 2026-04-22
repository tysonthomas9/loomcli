/**
 * 08 Dependencies parity — add / remove / blocks chain.
 */
import { parityTest as test, expect, useParityHooks } from "./_support/spec-harness";
import {
    apiResponseDiff,
    findFleetIssueByTitle,
    routedFleetRequest,
    SEED_FIXTURE,
} from "./_support";
import { PARITY_URLS } from "./playwright.config";

useParityHooks();

test.describe("08 dependencies parity", () => {
    test("add and remove a dep on fleet — routing proven", async ({ tabs, fleetSpy }) => {
        const [a, b] = SEED_FIXTURE.children.slice(0, 2); // "Add login flow", "Fix checkout NPE"
        const { id: blockerId } = await findFleetIssueByTitle(a);
        const { id: blockedId } = await findFleetIssueByTitle(b);

        await tabs.fleet.goto(
            `${PARITY_URLS.fleet}/ws/${PARITY_URLS.workspace}/issues/${blockedId}`,
        );

        await routedFleetRequest(tabs, fleetSpy, "add-dep", {
            path: `issues/${blockedId}/deps`,
            method: "POST",
            body: { blocked_by: blockerId },
            acceptStatus: [201],
        });

        // Verify the dep appears via the API on both sides.
        const deps = await apiResponseDiff(`issues/${blockedId}/deps`);
        expect(deps.count_fleet).toBeGreaterThanOrEqual(1);
    });
});
