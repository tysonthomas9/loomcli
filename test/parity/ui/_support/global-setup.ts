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

export default async function globalSetup(): Promise<void> {
    // eslint-disable-next-line no-console
    console.log("[global-setup] running parity preflight...");
    await preflight(); // throws on any failure — suite aborts.
    // eslint-disable-next-line no-console
    console.log("[global-setup] preflight OK; suite may proceed");
}
