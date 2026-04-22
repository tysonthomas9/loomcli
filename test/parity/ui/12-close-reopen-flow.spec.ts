/**
 * 12 Close / reopen parity — close reason displays; reopen clears.
 *
 * This spec is sensitive to a past divergence where bd returned
 * close_reason="Closed" regardless of input while fleet-db returned the
 * actual reason. Per webui-gaps.md "Known signals", that's now aligned;
 * we assert it here to catch regressions.
 */
import { parityTest as test, expect, useParityHooks } from "./_support/spec-harness";
import { assertRoutingForAction, SEED_FIXTURE } from "./_support";
import { PARITY_URLS } from "./playwright.config";

useParityHooks();

test.describe("12 close / reopen parity", () => {
    test("close shows reason, reopen clears it", async ({ tabs, fleetSpy }) => {
        const target = SEED_FIXTURE.children[5]; // "Update README"
        const j = await fetch(
            `${PARITY_URLS.fleet}/api/workspaces/${PARITY_URLS.workspace}/issues`,
        ).then((r) => r.json());
        const issue = (j.data ?? []).find((i: any) => i.title === target);
        expect(issue).toBeTruthy();

        await assertRoutingForAction(tabs.testId, "close-issue", fleetSpy, async () => {
            await tabs.fleet.evaluate(
                async ({ id, ws }) => {
                    const r = await fetch(`/api/workspaces/${ws}/issues/${id}/close`, {
                        method: "POST",
                        headers: { "Content-Type": "application/json" },
                        body: JSON.stringify({ close_reason: "fixed-by-parity-12" }),
                    });
                    if (!r.ok) throw new Error(`close failed: ${r.status}`);
                },
                { id: issue.id, ws: PARITY_URLS.workspace },
            );
        });

        // Fetch and verify close_reason.
        const closed = await fetch(
            `${PARITY_URLS.fleet}/api/workspaces/${PARITY_URLS.workspace}/issues/${issue.id}`,
        ).then((r) => r.json());
        const after = closed?.data ?? closed;
        expect(after.status).toBe("closed");
        expect(after.close_reason).toBe("fixed-by-parity-12");

        // Reopen — routing verified again.
        await assertRoutingForAction(tabs.testId, "reopen-issue", fleetSpy, async () => {
            await tabs.fleet.evaluate(
                async ({ id, ws }) => {
                    const r = await fetch(
                        `/api/workspaces/${ws}/issues/${id}/reopen`,
                        { method: "POST" },
                    );
                    if (!r.ok) throw new Error(`reopen failed: ${r.status}`);
                },
                { id: issue.id, ws: PARITY_URLS.workspace },
            );
        });
        const reopened = await fetch(
            `${PARITY_URLS.fleet}/api/workspaces/${PARITY_URLS.workspace}/issues/${issue.id}`,
        ).then((r) => r.json());
        const rAfter = reopened?.data ?? reopened;
        expect(rAfter.status).not.toBe("closed");
        // close_reason should be cleared or missing after reopen.
        expect(rAfter.close_reason ?? "").toBe("");
    });
});
