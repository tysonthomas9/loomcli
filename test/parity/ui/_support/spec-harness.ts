/**
 * Shared test harness used by every spec. Implements the §2 skeleton from
 * ui-test-plan.md so every spec body can focus on the action + assertions.
 *
 * Usage:
 *   import { parityTest } from "./_support/spec-harness";
 *   parityTest("does X", async ({ tabs, recordRouteHit }) => {
 *       ...
 *   });
 */
import { test as base, expect, BrowserContext, Page } from "@playwright/test";
import {
    openDualTabs,
    resetBothBackends,
    snapshotState,
    preflight,
    saveForensics,
    normalizeUrlToRoute,
    recordRoutes,
    attachFleetNetworkSpy,
    type DualTabs,
} from ".";

/** Extended fixture bag handed to each spec body. */
export interface ParityFixtures {
    tabs: DualTabs;
    /** Append a normalized route to the coverage record for this test. */
    recordRouteHit: (url: string) => void;
    /** Network spy on the fleet tab, attached automatically. */
    fleetSpy: ReturnType<typeof attachFleetNetworkSpy>;
    /** Pre-action state snapshot (populated in beforeEach). */
    stateBefore: Awaited<ReturnType<typeof snapshotState>>;
}

export const parityTest = base.extend<ParityFixtures>({
    tabs: async ({ browser }, use, testInfo) => {
        // Log the budget consumed by each phase of tabs setup so future
        // "timeout while setting up tabs" failures can be blamed on the
        // right step (HAR context, newPage, resetBothBackends, etc.)
        // rather than the umbrella fixture.
        const t0 = Date.now();
        const tabs = await openDualTabs(browser, testInfo.titlePath.join("::"));
        const openMs = Date.now() - t0;
        if (openMs > 2000) {
            // eslint-disable-next-line no-console
            console.log(`[tabs-fixture] openDualTabs took ${openMs}ms`);
        }
        // Track EVERY request so the coverage helper can normalize URLs on
        // both tabs without manual recordRouteHit calls in simple specs.
        const urls: string[] = [];
        const collect = (req: any) => urls.push(req.url());
        tabs.beads.on("request", collect);
        tabs.fleet.on("request", collect);
        try {
            await use(tabs);
        } finally {
            // Flush coverage for this test.
            const normalized = urls
                .map(normalizeUrlToRoute)
                .filter((r): r is string => !!r);
            recordRoutes(testInfo.titlePath.join(" > "), [...new Set(normalized)]);
            if (testInfo.status !== testInfo.expectedStatus) {
                await saveForensics(tabs.testId, tabs.beads, tabs.fleet);
            }
            await tabs.close();
        }
    },

    fleetSpy: async ({ tabs }, use) => {
        const spy = attachFleetNetworkSpy(tabs.fleet);
        await use(spy);
        spy.detach();
    },

    recordRouteHit: async ({}, use, testInfo) => {
        const hits: string[] = [];
        await use((url: string) => {
            const rt = normalizeUrlToRoute(url);
            if (rt) hits.push(rt);
        });
        if (hits.length > 0) {
            recordRoutes(`${testInfo.titlePath.join(" > ")} [manual]`, [
                ...new Set(hits),
            ]);
        }
    },

    stateBefore: async ({ tabs }, use, testInfo) => {
        const t0 = Date.now();
        const s = await snapshotState("before", tabs.testId);
        const snapMs = Date.now() - t0;
        if (snapMs > 3000) {
            // eslint-disable-next-line no-console
            console.log(`[stateBefore] snapshotState took ${snapMs}ms`);
        }
        await use(s);
    },
});

/**
 * Convenience: every spec's beforeAll calls this, and every spec's
 * beforeEach resets state. Both are idempotent.
 */
export function useParityHooks() {
    parityTest.beforeAll(async () => {
        // Skip sanity gate on operator request — see global-setup.ts.
        if (process.env.PARITY_SKIP_PREFLIGHT === "1") {
            return;
        }
        // Runs once per worker — preflight caches, so cheap to call again.
        await preflight();
    });

    parityTest.beforeEach(async ({}, testInfo) => {
        // The reseed runs `podman compose run --rm parity-seed`, which
        // typically eats ~15–20s of wall time on this machine — nearly a
        // third of the default 60s test timeout. Give each test 90s so
        // the actual test body (which has its own 15s waits for selectors
        // + network settle) isn't starved by beforeEach.
        testInfo.setTimeout(Math.max(testInfo.timeout, 90_000));
        const t0 = Date.now();
        await resetBothBackends({ reseed: true });
        const resetMs = Date.now() - t0;
        if (resetMs > 5000) {
            // eslint-disable-next-line no-console
            console.log(
                `[beforeEach] resetBothBackends took ${resetMs}ms (seed dominates)`,
            );
        }
    });
}

export { expect };
