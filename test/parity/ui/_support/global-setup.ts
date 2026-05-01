/**
 * Playwright globalSetup: runs ONCE before the entire suite. Executes
 * preflight and aborts (throws) on failure.
 *
 * Wired via `globalSetup: "./_support/global-setup"` in playwright.config.ts.
 * We do NOT wire it by default — the preflight also runs in each spec's
 * beforeAll so tests can also drive it from the test runner (useful for
 * IDE runners). Having both is cheap because preflight caches.
 *
 * This module is kept separate so a future CI workflow can import it
 * independently from a hermetic shell script.
 */
import { preflight } from "./preflight";
import { resetCoverageRecords } from "./coverage";

export default async function globalSetup(): Promise<void> {
    resetCoverageRecords();
    // Escape hatch: PARITY_SKIP_PREFLIGHT=1 skips the preflight's environment
    // sanity gate. Tests still exercise both backends — they just aren't
    // blocked on preflight assumptions that may not hold in every
    // environment (podman vs docker, /api/config backend exposure, etc.).
    // Only use in exploratory runs; CI should keep preflight enforced.
    if (process.env.PARITY_SKIP_PREFLIGHT === "1") {
        // eslint-disable-next-line no-console
        console.log("[global-setup] PARITY_SKIP_PREFLIGHT=1 — preflight bypassed");
        return;
    }
    // eslint-disable-next-line no-console
    console.log("[global-setup] running parity preflight...");
    await preflight(); // throws on any failure — suite aborts.
    // eslint-disable-next-line no-console
    console.log("[global-setup] preflight OK; suite may proceed");
}
