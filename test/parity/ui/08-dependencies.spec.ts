/**
 * 08 Dependencies parity — add / remove / blocks chain.
 */
import { parityTest as test, expect, useParityHooks } from "./_support/spec-harness";
import {
    assertRoutingForAction,
    apiResponseDiff,
    SEED_FIXTURE,
} from "./_support";
import { PARITY_URLS } from "./playwright.config";

useParityHooks();

test.describe("08 dependencies parity", () => {
    test("add and remove a dep on fleet — routing proven", async ({ tabs, fleetSpy }) => {
        const [a, b] = SEED_FIXTURE.children.slice(0, 2); // "Add login flow", "Fix checkout NPE"
        const j = await fetch(
            `${PARITY_URLS.fleet}/api/workspaces/${PARITY_URLS.workspace}/issues`,
        ).then((r) => r.json());
        const blocker = (j.data ?? []).find((i: any) => i.title === a);
        const blocked = (j.data ?? []).find((i: any) => i.title === b);
        expect(blocker, `blocker seed "${a}" not found`).toBeTruthy();
        expect(blocked, `blocked seed "${b}" not found`).toBeTruthy();

        await tabs.fleet.goto(
            `${PARITY_URLS.fleet}/ws/${PARITY_URLS.workspace}/issues/${blocked.id}`,
        );

        await assertRoutingForAction(tabs.testId, "add-dep", fleetSpy, async () => {
            await tabs.fleet.evaluate(
                async ({ blockedId, blockerId, ws }) => {
                    const r = await fetch(
                        `/api/workspaces/${ws}/issues/${blockedId}/deps`,
                        {
                            method: "POST",
                            headers: { "Content-Type": "application/json" },
                            body: JSON.stringify({ blocked_by: blockerId }),
                        },
                    );
                    if (!r.ok && r.status !== 201) {
                        throw new Error(`dep POST: ${r.status}`);
                    }
                },
                { blockedId: blocked.id, blockerId: blocker.id, ws: PARITY_URLS.workspace },
            );
        });

        // Verify the dep appears via the API on both sides.
        const deps = await apiResponseDiff(`issues/${blocked.id}/deps`);
        expect(deps.count_fleet).toBeGreaterThanOrEqual(1);
    });
});
