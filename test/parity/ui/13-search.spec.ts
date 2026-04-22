/**
 * 13 Search parity — full-text query returns the same result set.
 */
import { parityTest as test, expect, useParityHooks } from "./_support/spec-harness";
import { PARITY_URLS } from "./playwright.config";

useParityHooks();

test.describe("13 search parity", () => {
    test("same query returns same issues on both backends", async ({ tabs }) => {
        const q = "checkout"; // matches "Fix checkout NPE" from seed
        const [b, f] = await Promise.all([
            fetch(
                `${PARITY_URLS.beads}/api/workspaces/default/issues/search?q=${encodeURIComponent(q)}`,
            ).then((r) => (r.ok ? r.json() : { data: [] })),
            fetch(
                `${PARITY_URLS.fleet}/api/workspaces/${PARITY_URLS.workspace}/issues/search?q=${encodeURIComponent(q)}`,
            ).then((r) => (r.ok ? r.json() : { data: [] })),
        ]);
        const bTitles = (b.data ?? []).map((i: any) => i.title).sort();
        const fTitles = (f.data ?? []).map((i: any) => i.title).sort();
        expect(bTitles).toEqual(fTitles);
        expect(bTitles.some((t: string) => /checkout/i.test(t))).toBeTruthy();
    });
});
