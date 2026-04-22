/**
 * 11 Update flow parity — PATCH priority + description on fleet; state
 * reflects the update and routing proofs confirm fleet-db saw it.
 */
import { parityTest as test, expect, useParityHooks } from "./_support/spec-harness";
import { assertRoutingForAction, SEED_FIXTURE } from "./_support";
import { PARITY_URLS } from "./playwright.config";

useParityHooks();

test.describe("11 update-flow parity", () => {
    test("PATCH priority and description routes to fleet-db", async ({
        tabs,
        fleetSpy,
    }) => {
        const target = SEED_FIXTURE.children[2]; // "Refactor auth middleware"
        const j = await fetch(
            `${PARITY_URLS.fleet}/api/workspaces/${PARITY_URLS.workspace}/issues`,
        ).then((r) => r.json());
        const issue = (j.data ?? []).find((i: any) => i.title === target);
        expect(issue).toBeTruthy();

        await tabs.fleet.goto(
            `${PARITY_URLS.fleet}/ws/${PARITY_URLS.workspace}/issues/${issue.id}`,
        );

        await assertRoutingForAction(
            tabs.testId,
            "update-priority-description",
            fleetSpy,
            async () => {
                await tabs.fleet.evaluate(
                    async ({ id, ws }) => {
                        const r = await fetch(`/api/workspaces/${ws}/issues/${id}`, {
                            method: "PATCH",
                            headers: { "Content-Type": "application/json" },
                            body: JSON.stringify({
                                priority: 1,
                                description: "bumped to P1 via parity-11 test",
                            }),
                        });
                        if (!r.ok) throw new Error(`PATCH failed: ${r.status}`);
                    },
                    { id: issue.id, ws: PARITY_URLS.workspace },
                );
            },
        );

        // Verify via API that the change landed.
        const after = await fetch(
            `${PARITY_URLS.fleet}/api/workspaces/${PARITY_URLS.workspace}/issues/${issue.id}`,
        ).then((r) => r.json());
        const updated = after?.data ?? after;
        expect(updated.priority).toBe(1);
        expect(
            typeof updated.description === "string" &&
                updated.description.includes("bumped to P1"),
        ).toBeTruthy();
    });
});
