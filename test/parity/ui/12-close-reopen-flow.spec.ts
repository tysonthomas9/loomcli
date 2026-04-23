/**
 * 12 Close / reopen parity — close reason displays; reopen clears.
 *
 * This spec is sensitive to a past divergence where bd returned
 * close_reason="Closed" regardless of input while fleet-db returned the
 * actual reason. Per webui-gaps.md "Known signals", that's now aligned;
 * we assert it here to catch regressions.
 */
import { parityTest as test, expect, useParityHooks } from "./_support/spec-harness";
import { findFleetIssueByTitle, routedFleetRequest, SEED_FIXTURE } from "./_support";
import { PARITY_URLS } from "./playwright.config";

useParityHooks();

test.describe("12 close / reopen parity", () => {
    test("close shows reason, reopen clears it", async ({ tabs, fleetSpy }) => {
        const { id } = await findFleetIssueByTitle(SEED_FIXTURE.children[5]); // "Update README"

        // routedFleetRequest evaluates fetch("/api/...") inside tabs.fleet's
        // page context; a freshly-created BrowserContext page is on
        // about:blank, so the relative URL has no origin to resolve
        // against and the fetch throws "Failed to parse URL from …".
        // Other mutation specs do this goto for the same reason (see 07,
        // 08, 10, 11) — this one was missing it.
        await tabs.fleet.goto(
            `${PARITY_URLS.fleet}/ws/${PARITY_URLS.workspace}/issues/${id}`,
        );

        await routedFleetRequest(tabs, fleetSpy, "close-issue", {
            path: `issues/${id}/close`,
            method: "POST",
            body: { close_reason: "fixed-by-parity-12" },
        });

        // Fetch and verify close_reason.
        const closed = await fetch(
            `${PARITY_URLS.fleet}/api/workspaces/${PARITY_URLS.workspace}/issues/${id}`,
        ).then((r) => r.json());
        const after = closed?.data ?? closed;
        expect(after.status).toBe("closed");
        expect(after.close_reason).toBe("fixed-by-parity-12");

        // Reopen — routing verified again. Body is deliberately absent so the
        // helper emits a header-only POST matching the production UI.
        await routedFleetRequest(tabs, fleetSpy, "reopen-issue", {
            path: `issues/${id}/reopen`,
            method: "POST",
        });
        const reopened = await fetch(
            `${PARITY_URLS.fleet}/api/workspaces/${PARITY_URLS.workspace}/issues/${id}`,
        ).then((r) => r.json());
        const rAfter = reopened?.data ?? reopened;
        expect(rAfter.status).not.toBe("closed");
        // close_reason should be cleared or missing after reopen.
        expect(rAfter.close_reason ?? "").toBe("");
    });
});
