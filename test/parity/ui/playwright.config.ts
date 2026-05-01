/**
 * Playwright config for the beads-vs-fleet UI parity suite.
 *
 * Paired with docker-compose.parity.yml. Assumes the stack is already up
 * (see test/parity/ui/README.md or ui-test-plan.md §0). The suite refuses
 * to run if preflight checks fail — see _support/preflight.ts.
 *
 * Environment overrides:
 *   LOOM_BEADS_URL  default http://localhost:8081
 *   LOOM_FLEET_URL  default http://localhost:8082
 *   FLEET_DB_URL    default http://localhost:8080
 *   REDIS_URL       default redis://localhost:6379 (used by redis-cli probe)
 *   PARITY_WORKSPACE default PARITY
 *   PARITY_MODE     dual (default) or fleet-only
 *   PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH  to pin Docker chromium
 */
import { defineConfig, devices } from "@playwright/test";
import * as path from "path";
import { isFleetOnlyMode, parityMode } from "./_support/mode";

const chromiumExecutablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH;

// Fixed viewport + DPI + fonts keep screenshots deterministic across
// operator machines. Do NOT change these lightly; baselines depend on them.
const VIEWPORT = { width: 1280, height: 720 };
const DPR = 2;

export default defineConfig({
    testDir: ".",
    testMatch: /\d{2}-.*\.spec\.ts$/,
    fullyParallel: false, // Lockstep dual-tab driving; parallel workers confuse seed state.
    workers: 1,
    retries: 0, // Parity is correctness testing — retries would mask flakes.
    timeout: 60_000,
    globalTimeout: 30 * 60_000,
    globalSetup: "./_support/global-setup.ts",
    globalTeardown: "./_support/global-teardown.ts",

    expect: {
        timeout: 10_000,
        toHaveScreenshot: {
            // 2% threshold per plan §4; anti-aliasing on different hosts
            // easily eats 1% even on identical renders.
            maxDiffPixelRatio: 0.02,
            threshold: 0.2,
            animations: "disabled",
        },
    },

    reporter: [
        ["list"],
        ["html", { outputFolder: "artifacts/reports/html", open: "never" }],
        ["json", { outputFile: "artifacts/reports/results.json" }],
    ],

    outputDir: "artifacts/test-output",

    use: {
        viewport: VIEWPORT,
        deviceScaleFactor: DPR,
        // Consistent locale/timezone prevents date-format drift in
        // screenshots between operator machines and CI runners.
        locale: "en-US",
        timezoneId: "UTC",
        // Capture enough forensics by default to reconstruct a failure
        // without rerun.
        trace: "retain-on-failure",
        screenshot: "only-on-failure",
        video: "retain-on-failure",
        // Serialize XHR intercepts; one-action-per-test discipline.
        actionTimeout: 15_000,
        navigationTimeout: 30_000,
        // Disable smooth scrolling + animations so screenshots match.
        launchOptions: {
            args: ["--font-render-hinting=none"],
            ...(chromiumExecutablePath ? { executablePath: chromiumExecutablePath } : {}),
        },
    },

    projects: [
        {
            name: "parity-chromium",
            use: {
                ...devices["Desktop Chrome"],
                viewport: VIEWPORT,
                deviceScaleFactor: DPR,
            },
        },
    ],

    // No webServer — the stack is managed externally via docker-compose.
    // preflight.ts enforces this; see _support/preflight.ts.
});

// Re-exported for specs and helpers so everyone reads the same envs.
//
// Defaults point at the Caddy UI sidecars (8083 fleet, 8084 beads) which
// serve the prebuilt frontend AND proxy /api/* to the underlying loom
// container. Specs that want to bypass the proxy and hit loom directly
// can override via LOOM_BEADS_URL / LOOM_FLEET_URL → "http://localhost:8081"
// or 8082. fleetDB stays at 8080 since several tests poke fleet-db
// directly to confirm the through-loom path actually wrote.
export const PARITY_URLS = {
    fleet: process.env.LOOM_FLEET_URL ?? "http://localhost:8083",
    fleetDB: process.env.FLEET_DB_URL ?? "http://localhost:8080",
    redis: process.env.REDIS_URL ?? "redis://localhost:6379",
    workspace: process.env.PARITY_WORKSPACE ?? "PARITY",
    mode: parityMode(),
    // In fleet-only regression mode the "beads/reference" tab is a second
    // fleet tab. This lets most side-by-side specs continue exercising the
    // same UI paths without starting loom-beads or bd.
    beads: isFleetOnlyMode()
        ? (process.env.LOOM_FLEET_URL ?? "http://localhost:8083")
        : (process.env.LOOM_BEADS_URL ?? "http://localhost:8084"),
};

export const ARTIFACTS_DIR = path.resolve(__dirname, "artifacts");
