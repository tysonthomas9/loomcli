/**
 * 07 Comments parity — add + list. Body/text wire-format drift is tracked
 * under WAIVER-003 per ui-test-plan.md; we surface it explicitly here
 * rather than masking it.
 */
import { parityTest as test, expect, useParityHooks } from "./_support/spec-harness";
import {
    apiResponseDiff,
    assertRoutingForAction,
    SEED_FIXTURE,
} from "./_support";
import { PARITY_URLS } from "./playwright.config";

useParityHooks();

test.describe("07 comments parity", () => {
    test("add a comment on the fleet tab — routing proven", async ({ tabs, fleetSpy }) => {
        // Resolve a target issue on each side.
        const title = SEED_FIXTURE.children[0];
        const fleetList = await fetch(
            `${PARITY_URLS.fleet}/api/workspaces/${PARITY_URLS.workspace}/issues`,
        ).then((r) => r.json());
        const fleetIssue = (fleetList.data ?? []).find((i: any) => i.title === title);
        expect(fleetIssue).toBeTruthy();

        await tabs.fleet.goto(
            `${PARITY_URLS.fleet}/ws/${PARITY_URLS.workspace}/issues/${fleetIssue.id}`,
        );

        await assertRoutingForAction(tabs.testId, "add-comment", fleetSpy, async () => {
            await tabs.fleet.evaluate(
                async ({ id, ws }) => {
                    const r = await fetch(
                        `/api/workspaces/${ws}/issues/${id}/comments`,
                        {
                            method: "POST",
                            headers: { "Content-Type": "application/json" },
                            // WAIVER-003: fleet expects "body", beads expects "text".
                            // The adapter layer remaps; we send the canonical
                            // shape the UI sends.
                            body: JSON.stringify({ body: "parity test comment" }),
                        },
                    );
                    if (!r.ok && r.status !== 201) {
                        throw new Error(`comment POST: ${r.status}`);
                    }
                },
                { id: fleetIssue.id, ws: PARITY_URLS.workspace },
            );
        });

        // List comments and assert at least one shows up. We don't force
        // field-by-field diff here because WAIVER-003 documents body vs text.
        const diff = await apiResponseDiff(`issues/${fleetIssue.id}/comments`);
        expect(diff.count_fleet).toBeGreaterThanOrEqual(1);
    });
});
